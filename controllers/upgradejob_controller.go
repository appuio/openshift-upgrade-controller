package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"go.uber.org/multierr"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
	"github.com/appuio/openshift-upgrade-controller/pkg/healthcheck"
)

// UpgradeJobReconciler reconciles a UpgradeJob object
type UpgradeJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Clock Clock

	ManagedUpstreamClusterVersionName string
}

var ClusterVersionLockAnnotation = managedupgradev1beta1.GroupVersion.Group + "/upgrade-job"

const (
	UpgradeJobHookJobTrackerFinalizer = "upgradejobs.managedupgrade.appuio.io/hook-job-tracker"

	hookJobTrackingLabelUpgradeJobHook = "upgradejobs.managedupgrade.appuio.io/upgradejobhook"
	hookJobTrackingLabelUpgradeJob     = "upgradejobs.managedupgrade.appuio.io/upgradejobk"
	hookJobTrackingLabelEvent          = "upgradejobs.managedupgrade.appuio.io/event"
)

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobs/finalizers,verbs=update

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobhooks,verbs=get;list;watch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobhooks/status,verbs=get;update;patch

//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/finalizers,verbs=update

// Reconcile reconciles a UpgradeJob object and starts the upgrade if necessary.
func (r *UpgradeJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("UpgradeJobReconciler.Reconcile")
	l.Info("Reconciling UpgradeJob")

	if err := r.trackHookJobs(ctx, req); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to track hook jobs: %w", err)
	}

	var uj managedupgradev1beta1.UpgradeJob
	if err := r.Get(ctx, req.NamespacedName, &uj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !uj.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if apimeta.IsStatusConditionTrue(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded) {
		// Ignore hooks status, they can't influence the upgrade anymore.
		_, eserr := r.executeHooks(ctx, &uj, managedupgradev1beta1.EventSuccess)
		_, eferr := r.executeHooks(ctx, &uj, managedupgradev1beta1.EventFinish)
		return ctrl.Result{}, multierr.Combine(eserr, eferr, r.cleanupLock(ctx, &uj))
	}
	if apimeta.IsStatusConditionTrue(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed) {
		// Ignore hooks status, they can't influence the upgrade anymore.
		_, efaerr := r.executeHooks(ctx, &uj, managedupgradev1beta1.EventFailure)
		_, efierr := r.executeHooks(ctx, &uj, managedupgradev1beta1.EventFinish)
		return ctrl.Result{}, multierr.Combine(efaerr, efierr, r.cleanupLock(ctx, &uj))
	}

	cont, err := r.executeHooks(ctx, &uj, managedupgradev1beta1.EventCreate)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !cont {
		return ctrl.Result{}, nil
	}

	if apimeta.IsStatusConditionTrue(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted) {
		return r.reconcileStartedJob(ctx, &uj)
	}

	now := r.Clock.Now()

	if now.After(uj.Spec.StartBefore.Time) {
		r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
			Type:    managedupgradev1beta1.UpgradeJobConditionFailed,
			Status:  metav1.ConditionTrue,
			Reason:  managedupgradev1beta1.UpgradeJobReasonExpired,
			Message: fmt.Sprintf("Job could not be started before %s", uj.Spec.StartBefore.Format(time.RFC3339)),
		})

		return ctrl.Result{}, r.Status().Update(ctx, &uj)
	}

	if !now.Before(uj.Spec.StartAfter.Time) {
		r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
			Type:    managedupgradev1beta1.UpgradeJobConditionStarted,
			Status:  metav1.ConditionTrue,
			Reason:  managedupgradev1beta1.UpgradeJobReasonStarted,
			Message: fmt.Sprintf("Upgrade started at %s", now.Format(time.RFC3339)),
		})

		return ctrl.Result{}, r.Status().Update(ctx, &uj)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: uj.Spec.StartAfter.Time.Sub(now)}, nil
}

func (r *UpgradeJobReconciler) reconcileStartedJob(ctx context.Context, uj *managedupgradev1beta1.UpgradeJob) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("UpgradeJobReconciler.reconcileStartedJob")

	cont, err := r.executeHooks(ctx, uj, managedupgradev1beta1.EventStart)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !cont {
		return ctrl.Result{}, nil
	}

	startedCond := apimeta.FindStatusCondition(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted)
	if r.Clock.Now().After(startedCond.LastTransitionTime.Add(uj.Spec.UpgradeTimeout.Duration)) {
		r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
			Type:    managedupgradev1beta1.UpgradeJobConditionFailed,
			Status:  metav1.ConditionTrue,
			Reason:  managedupgradev1beta1.UpgradeJobReasonTimedOut,
			Message: fmt.Sprintf("Upgrade timed out after %s", uj.Spec.UpgradeTimeout.Duration.String()),
		})
		return ctrl.Result{}, r.Status().Update(ctx, uj)
	}

	var version configv1.ClusterVersion
	if err := r.Get(ctx, types.NamespacedName{
		Name: r.ManagedUpstreamClusterVersionName,
	}, &version); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster version: %w", err)
	}

	// Lock the cluster version to prevent other upgrade jobs from starting
	if err := r.tryLockClusterVersion(ctx, &version, uj.Namespace+"/"+uj.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to lock cluster version: %w", err)
	}

	ok, err := r.runHealthCheck(ctx, uj, version,
		uj.Spec.UpgradeJobConfig.PreUpgradeHealthChecks,
		managedupgradev1beta1.UpgradeJobConditionPreHealthCheckDone,
		managedupgradev1beta1.UpgradeJobReasonPreHealthCheckFailed)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to run pre-upgrade health checks: %w", err)
	}
	if !ok {
		return ctrl.Result{}, nil
	}

	if uj.Spec.DesiredVersion != nil {
		// Check if the desired version is already set
		if version.Spec.DesiredUpdate == nil || *version.Spec.DesiredUpdate != *uj.Spec.DesiredVersion {
			update := clusterversion.FindAvailableUpdate(version, uj.Spec.DesiredVersion.Image, uj.Spec.DesiredVersion.Version)
			if update == nil {
				r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
					Type:    managedupgradev1beta1.UpgradeJobConditionFailed,
					Status:  metav1.ConditionTrue,
					Reason:  managedupgradev1beta1.UpgradeJobReasonUpgradeWithdrawn,
					Message: fmt.Sprintf("Upgrade became unavailable: %s", uj.Spec.DesiredVersion.Version),
				})
				return ctrl.Result{}, r.Status().Update(ctx, uj)
			}
			// Start the upgrade
			version.Spec.DesiredUpdate = uj.Spec.DesiredVersion
			if err := r.Update(ctx, &version); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update desired version in cluster version: %w", err)
			}
			return ctrl.Result{}, nil
		}

		// Check if the upgrade is done
		upgradedCon := apimeta.FindStatusCondition(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted)
		if upgradedCon == nil {
			r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
				Type:    managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted,
				Status:  metav1.ConditionFalse,
				Reason:  managedupgradev1beta1.UpgradeJobReasonInProgress,
				Message: "Upgrade in progress",
			})
			return ctrl.Result{}, r.Status().Update(ctx, uj)
		}
		if upgradedCon.Status != metav1.ConditionTrue {
			if !clusterversion.IsVersionUpgradeCompleted(version) {
				l.Info("Upgrade still in progress")
				return ctrl.Result{}, nil
			}

			mcpl := machineconfigurationv1.MachineConfigPoolList{}
			if err := r.List(ctx, &mcpl); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list machine config pools: %w", err)
			}
			poolsUpdating := healthcheck.MachineConfigPoolsUpdating(mcpl)
			if len(poolsUpdating) > 0 {
				l.Info("Machine config pools still updating", "pools", poolsUpdating)
				return ctrl.Result{}, nil
			}

			r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
				Type:    managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted,
				Status:  metav1.ConditionTrue,
				Reason:  managedupgradev1beta1.UpgradeJobReasonCompleted,
				Message: "Upgrade completed",
			})
			return ctrl.Result{}, r.Status().Update(ctx, uj)
		}
	}

	ok, err = r.runHealthCheck(ctx, uj, version,
		uj.Spec.UpgradeJobConfig.PostUpgradeHealthChecks,
		managedupgradev1beta1.UpgradeJobConditionPostHealthCheckDone,
		managedupgradev1beta1.UpgradeJobReasonPostHealthCheckFailed)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to run post-upgrade health checks: %w", err)
	}
	if !ok {
		return ctrl.Result{}, nil
	}

	cont, err = r.executeHooks(ctx, uj, managedupgradev1beta1.EventUpgradeComplete)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !cont {
		return ctrl.Result{}, nil
	}

	// Set the upgrade as successful
	r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
		Type:    managedupgradev1beta1.UpgradeJobConditionSucceeded,
		Status:  metav1.ConditionTrue,
		Reason:  managedupgradev1beta1.UpgradeJobReasonSucceeded,
		Message: "Upgrade succeeded",
	})
	if err := r.Status().Update(ctx, uj); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set job as succeeded: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UpgradeJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	jm := handler.EnqueueRequestsFromMapFunc(JobFromClusterVersionMapper(mgr.GetClient(), r.ManagedUpstreamClusterVersionName))
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.UpgradeJob{}).
		Watches(&configv1.ClusterVersion{}, jm).
		Watches(&machineconfigurationv1.MachineConfigPool{}, jm).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// JobFromClusterVersionMapper returns the job locking the cluster version or nothing.
// The given object is ignored.
func JobFromClusterVersionMapper(c client.Reader, cvName string) handler.MapFunc {
	return func(ctx context.Context, _ client.Object) []reconcile.Request {
		l := log.FromContext(ctx).WithName("JobFromClusterVersionHandler")

		var version configv1.ClusterVersion
		if err := c.Get(ctx, types.NamespacedName{Name: cvName}, &version); err != nil {
			l.Error(err, "failed to get cluster version")
			return nil
		}

		found, namespacedName := upgradeJobNameFromLockedClusterVersion(version)
		if !found {
			return nil
		}

		return []reconcile.Request{{NamespacedName: namespacedName}}
	}
}

// upgradeJobNameFromLockedClusterVersion returns the upgrade job name from the locked cluster version.
// If the cluster version is not locked, it returns false.
func upgradeJobNameFromLockedClusterVersion(cv configv1.ClusterVersion) (ok bool, nn types.NamespacedName) {
	job := cv.GetAnnotations()[ClusterVersionLockAnnotation]
	if job == "" {
		return false, types.NamespacedName{}
	}

	jobParts := strings.Split(job, "/")
	if len(jobParts) != 2 {
		return false, types.NamespacedName{}
	}

	return true, types.NamespacedName{
		Namespace: jobParts[0],
		Name:      jobParts[1],
	}
}

// setStatusCondition is a wrapper for apimeta.SetStatusCondition that sets the LastTransitionTime to r.Clock.Now().
func (r *UpgradeJobReconciler) setStatusCondition(conditions *[]metav1.Condition, newCondition metav1.Condition) {
	newCondition.LastTransitionTime = metav1.NewTime(r.Clock.Now())
	apimeta.SetStatusCondition(conditions, newCondition)
}

// runHealthCheck runs the health check for the given health check type and returns true if the health check is done.
func (r *UpgradeJobReconciler) runHealthCheck(
	ctx context.Context,
	uj *managedupgradev1beta1.UpgradeJob,
	version configv1.ClusterVersion,
	healthConfig managedupgradev1beta1.UpgradeJobHealthCheck,
	healthConditionType string,
	jobHealthFailedReason string,
) (bool, error) {

	healthCond := apimeta.FindStatusCondition(uj.Status.Conditions, healthConditionType)
	if healthCond == nil {
		// Record the start of the health checks for time-out purposes
		r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
			Type:    healthConditionType,
			Status:  metav1.ConditionFalse,
			Message: "Health checks started",
			Reason:  managedupgradev1beta1.UpgradeJobReasonInProgress,
		})
		return false, r.Status().Update(ctx, uj)
	}
	if healthCond.Status == metav1.ConditionTrue {
		return true, nil
	}
	healthy := healthConfig.SkipDegradedOperatorsCheck || !healthcheck.IsOperatorDegraded(version)
	if !healthy {
		if r.Clock.Now().After(healthCond.LastTransitionTime.Add(healthConfig.Timeout.Duration)) {
			r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
				Type:    managedupgradev1beta1.UpgradeJobConditionFailed,
				Status:  metav1.ConditionTrue,
				Reason:  jobHealthFailedReason,
				Message: fmt.Sprintf("Health checks timed out after %s", healthConfig.Timeout.Duration.String()),
			})
			return false, r.Status().Update(ctx, uj)
		}
		return false, nil
	}
	r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
		Type:    healthConditionType,
		Status:  metav1.ConditionTrue,
		Message: "Health checks ok",
		Reason:  managedupgradev1beta1.UpgradeJobReasonCompleted,
	})
	return true, r.Status().Update(ctx, uj)
}

func (r *UpgradeJobReconciler) cleanupLock(ctx context.Context, uj *managedupgradev1beta1.UpgradeJob) error {
	var version configv1.ClusterVersion
	if err := r.Get(ctx, types.NamespacedName{
		Name: r.ManagedUpstreamClusterVersionName,
	}, &version); err != nil {
		return fmt.Errorf("failed to get cluster version: %w", err)
	}

	lockingJob, hasLockingJob := version.Annotations[ClusterVersionLockAnnotation]
	if hasLockingJob && lockingJob == uj.Namespace+"/"+uj.Name {
		delete(version.Annotations, ClusterVersionLockAnnotation)
		if err := r.Update(ctx, &version); err != nil {
			return fmt.Errorf("failed to unlock cluster version: %w", err)
		}
	}

	return nil
}

// tryLockClusterVersion tries to lock the cluster version for the given upgrade job
func (r *UpgradeJobReconciler) tryLockClusterVersion(ctx context.Context, version *configv1.ClusterVersion, lockVal string) error {
	if version.Annotations == nil {
		version.Annotations = map[string]string{}
	}

	lockingJob, hasLockingJob := version.Annotations[ClusterVersionLockAnnotation]
	if hasLockingJob && lockingJob != lockVal {
		return fmt.Errorf("cluster version is locked by %s", lockingJob)
	} else if !hasLockingJob {
		version.Annotations[ClusterVersionLockAnnotation] = lockVal
		// There is no race condition between the Get and Update calls because the server will reject the update with a Conflict error if the resource has been modified since the Get call.
		if err := r.Client.Update(ctx, version); err != nil {
			return fmt.Errorf("failed to lock cluster version: %w", err)
		}
	}

	return nil
}

func (r *UpgradeJobReconciler) executeHooks(ctx context.Context, uj *managedupgradev1beta1.UpgradeJob, event managedupgradev1beta1.UpgradeEvent) (bool, error) {
	l := log.FromContext(ctx)

	var allHooks managedupgradev1beta1.UpgradeJobHookList
	if err := r.List(ctx, &allHooks, client.InNamespace(uj.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list hooks: %w", err)
	}

	hooks := make([]managedupgradev1beta1.UpgradeJobHook, 0, len(allHooks.Items))
	for _, hook := range allHooks.Items {
		if !slices.Contains(hook.Spec.Events, event) {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(&hook.Spec.Selector)
		if err != nil {
			l.Error(err, "failed to parse hook selector")
			continue
		}
		if !sel.Matches(labels.Set(uj.Labels)) {
			continue
		}
		if ok, upd := hook.Claim(uj); ok {
			if upd {
				l.Info("claimed hook", "hook", hook.Name)
				if err := r.Status().Update(ctx, &hook); err != nil {
					return false, err
				}
			}
			hooks = append(hooks, hook)
		}
	}

	activeJobs := []string{}
	errors := []error{}
	failedJobs := []string{}
	for _, hook := range hooks {
		jobs, err := r.jobForUpgradeJobAndHook(ctx, uj, hook, event)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		for _, job := range jobs {

			if job.Status == managedupgradev1beta1.HookJobTrackerStatusActive {
				activeJobs = append(activeJobs, job.UpgradeJobHookName)
				continue
			}

			if job.Status == managedupgradev1beta1.HookJobTrackerStatusFailed && hook.Spec.GetFailurePolicy() == managedupgradev1beta1.FailurePolicyIgnore {
				l.Info("hook failed but failure policy is ignore", "hook", hook.Name, "message", job.Message)
			} else if job.Status == managedupgradev1beta1.HookJobTrackerStatusFailed {
				failedJobs = append(failedJobs, fmt.Sprintf("hook %q failed: %s", hook.Name, job.Message))
			}
		}
	}
	if err := multierr.Combine(errors...); err != nil {
		return false, err
	}

	if len(activeJobs) > 0 {
		l.Info("waiting for hooks to complete", "activeJobs", activeJobs)
		return false, nil
	}

	if len(failedJobs) > 0 && event.InfluencesOutcome() {
		r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
			Type:    managedupgradev1beta1.UpgradeJobConditionFailed,
			Status:  metav1.ConditionTrue,
			Reason:  managedupgradev1beta1.UpgradeJobReasonHookFailed,
			Message: strings.Join(failedJobs, ", "),
		})
		return false, r.Status().Update(ctx, uj)
	} else if len(failedJobs) > 0 {
		l.Info("hooks failed but event does not influence outcome", "failedJobs", failedJobs)
	}

	return true, nil
}

func (r *UpgradeJobReconciler) jobForUpgradeJobAndHook(ctx context.Context, uj *managedupgradev1beta1.UpgradeJob, hook managedupgradev1beta1.UpgradeJobHook, event managedupgradev1beta1.UpgradeEvent) ([]managedupgradev1beta1.HookJobTracker, error) {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(uj.Namespace), client.MatchingLabels(jobLabels(uj.Name, hook.Name, event))); err != nil {
		return nil, err
	}

	jobStatus := findTrackedHookJob(hook.Name, string(event), *uj)
	slices.Grow(jobStatus, len(jobs.Items))
	for _, job := range jobs.Items {
		st, msg := hookStatusFromJob(job)
		jobStatus = append(jobStatus, managedupgradev1beta1.HookJobTracker{
			HookEvent:          string(event),
			UpgradeJobHookName: hook.Name,
			Status:             st,
			Message:            msg,
		})
	}

	if len(jobStatus) > 0 {
		return jobStatus, nil
	}

	_, err := r.createHookJob(ctx, hook, uj, event)
	return []managedupgradev1beta1.HookJobTracker{{
		HookEvent:          string(event),
		UpgradeJobHookName: hook.Name,
		Status:             managedupgradev1beta1.HookJobTrackerStatusActive,
	}}, err
}

func (r *UpgradeJobReconciler) createHookJob(ctx context.Context, hook managedupgradev1beta1.UpgradeJobHook, uj *managedupgradev1beta1.UpgradeJob, event managedupgradev1beta1.UpgradeEvent) (batchv1.Job, error) {
	l := log.FromContext(ctx)
	tmpl := hook.Spec.Template.DeepCopy()

	ll := make(map[string]string)
	maps.Copy(ll, tmpl.Labels)
	maps.Copy(ll, jobLabels(uj.Name, hook.Name, event))

	normalizedEvent := map[string]any{
		"name": string(event),
	}

	normalizedUJ, err := normalizeAsJson(uj)
	if err != nil {
		return batchv1.Job{}, fmt.Errorf("failed to normalize upgrade job: %w", err)
	}

	evm := map[string]any{
		"EVENT": normalizedEvent,
		"JOB":   normalizedUJ,
	}

	flattenInto("EVENT", normalizedEvent, evm)
	flattenInto("JOB", normalizedUJ, evm)

	envs := make([]corev1.EnvVar, 0, len(evm))
	for k, v := range evm {
		mv, err := json.Marshal(v)
		if err != nil {
			l.Info("failed to marshal value", "key", k, "value", v, "error", err)
			continue
		}
		envs = append(envs, corev1.EnvVar{Name: k, Value: string(mv)})
	}
	slices.SortFunc(envs, func(a, b corev1.EnvVar) int {
		return strings.Compare(a.Name, b.Name)
	})

	for i := range tmpl.Spec.Template.Spec.InitContainers {
		tmpl.Spec.Template.Spec.InitContainers[i].Env = append(tmpl.Spec.Template.Spec.InitContainers[i].Env, envs...)
	}
	for i := range tmpl.Spec.Template.Spec.Containers {
		tmpl.Spec.Template.Spec.Containers[i].Env = append(tmpl.Spec.Template.Spec.Containers[i].Env, envs...)
	}

	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName(uj.Name, hook.Name, string(event), "hook"),
			Namespace:   uj.Namespace,
			Annotations: tmpl.Annotations,
			Labels:      ll,
			Finalizers:  []string{UpgradeJobHookJobTrackerFinalizer},
		},
		Spec: tmpl.Spec,
	}

	if err := ctrl.SetControllerReference(uj, &job, r.Scheme); err != nil {
		return batchv1.Job{}, fmt.Errorf("failed to set controller reference: %w", err)
	}

	if err := r.Create(ctx, &job); err != nil {
		return batchv1.Job{}, fmt.Errorf("failed to create hook job: %w", err)
	}

	return job, nil
}

func jobName(elems ...string) string {
	name := strings.Join(elems, "-")
	name = strings.ToLower(name)
	if len(name) > 63 {
		digest := sha256.Sum256([]byte(name))
		return name[0:52] + "-" + hex.EncodeToString(digest[0:])[0:10]
	}
	return name
}

func jobLabels(jobName, hookName string, event managedupgradev1beta1.UpgradeEvent) map[string]string {
	return map[string]string{
		hookJobTrackingLabelUpgradeJobHook: hookName,
		hookJobTrackingLabelUpgradeJob:     jobName,
		hookJobTrackingLabelEvent:          string(event),
	}
}

// isJobFinished checks whether the given Job has finished execution.
// It does not discriminate between successful and failed terminations.
func isJobFinished(j batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// jobFailedCondition returns the JobCondition with type JobFailed if it exists and is true, nil otherwise.
func jobFailedCondition(j batchv1.Job) *batchv1.JobCondition {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return &c
		}
	}
	return nil
}

func normalizeAsJson(obj any) (normalized map[string]any, err error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(b, &normalized)
	return
}

// flattenInto takes normalized JSON and flattens objects into target.
// For example, {"a": {"b": 1}} becomes {"a_b": 1}.
func flattenInto(prefix string, obj any, target map[string]any) {
	fmtPrefix := func(s string) string {
		if s != "" {
			s += "_"
		}
		return s
	}

	switch o := obj.(type) {
	case map[string]any:
		for k, v := range o {
			flattenInto(fmtPrefix(prefix)+cleanEnvVarName(k), v, target)
		}
	case []any:
		for i, v := range o {
			flattenInto(fmtPrefix(prefix)+strconv.Itoa(i), v, target)
		}
	default:
		target[prefix] = o
	}
}

var envVarAllowedChars = regexp.MustCompile("[^a-zA-Z0-9_]")

func cleanEnvVarName(name string) string {
	return envVarAllowedChars.ReplaceAllString(name, "_")
}

func (r *UpgradeJobReconciler) trackHookJobs(ctx context.Context, req ctrl.Request) error {
	l := log.FromContext(ctx).WithName("UpgradeJobReconciler.trackHookJobs")
	var uj managedupgradev1beta1.UpgradeJob
	err := r.Get(ctx, req.NamespacedName, &uj)

	if err != nil && apierrors.IsNotFound(err) {
		//
		l.Info("upgrade job not found, cleaning tracking finalizers")
		return r.cleanupHookJobTrackingFinalizers(ctx, req.NamespacedName)
	} else if err != nil {
		return err
	}

	var jl batchv1.JobList
	if err := r.List(ctx, &jl, client.MatchingLabels{hookJobTrackingLabelUpgradeJob: uj.Name}, client.InNamespace(uj.Namespace)); err != nil {
		return err
	}

	tr := make([]managedupgradev1beta1.HookJobTracker, 0, len(jl.Items))
	for _, j := range jl.Items {
		s := managedupgradev1beta1.HookJobTracker{
			HookEvent:          j.Labels[hookJobTrackingLabelEvent],
			UpgradeJobHookName: j.Labels[hookJobTrackingLabelUpgradeJobHook],
		}

		s.Status, s.Message = hookStatusFromJob(j)

		tr = append(tr, s)
	}

	if !sets.New(uj.Status.HookJobTracker...).Equal(sets.New(tr...)) {
		uj.Status.HookJobTracker = tr
		if err := r.Status().Update(ctx, &uj); err != nil {
			return err
		}
	}

	// After updating the status we can remove the finalizers from finished jobs.
	// note: We should not reload the jobs here, as we might miss jobs that were just completed while
	// we were updating the status.
	errs := make([]error, 0, len(jl.Items))
	for _, j := range jl.Items {
		if !isJobFinished(j) {
			continue
		}
		if controllerutil.RemoveFinalizer(&j, UpgradeJobHookJobTrackerFinalizer) {
			errs = append(errs, r.Update(ctx, &j))
		}
	}

	return multierr.Combine(errs...)
}

func hookStatusFromJob(j batchv1.Job) (status managedupgradev1beta1.HookJobTrackerStatus, msg string) {
	status = managedupgradev1beta1.HookJobTrackerStatusActive
	if isJobFinished(j) {
		status = managedupgradev1beta1.HookJobTrackerStatusComplete
		if c := jobFailedCondition(j); c != nil {
			status = managedupgradev1beta1.HookJobTrackerStatusFailed
			msg = c.Message
		}
	}
	return
}

func (r *UpgradeJobReconciler) cleanupHookJobTrackingFinalizers(ctx context.Context, nn types.NamespacedName) error {
	var jl batchv1.JobList
	if err := r.List(ctx, &jl, client.MatchingLabels{hookJobTrackingLabelUpgradeJob: nn.Name}, client.InNamespace(nn.Namespace)); err != nil {
		return err
	}

	errs := make([]error, 0, len(jl.Items))
	for _, j := range jl.Items {
		if controllerutil.RemoveFinalizer(&j, UpgradeJobHookJobTrackerFinalizer) {
			errs = append(errs, r.Update(ctx, &j))
		}
	}

	return multierr.Combine(errs...)
}

func findTrackedHookJob(ujhookName, event string, uj managedupgradev1beta1.UpgradeJob) []managedupgradev1beta1.HookJobTracker {
	f := make([]managedupgradev1beta1.HookJobTracker, 0, len(uj.Status.HookJobTracker))
	for _, h := range uj.Status.HookJobTracker {
		if h.UpgradeJobHookName == ujhookName && h.HookEvent == event {
			f = append(f, h)
		}
	}
	return f
}
