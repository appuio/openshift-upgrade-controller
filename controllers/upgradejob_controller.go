package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"go.uber.org/multierr"
	"golang.org/x/exp/maps"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/strings/slices"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

const UpgradeJobHookJobFinalizer = "upgradejobhooks.managedupgrade.appuio.io/job"

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobs/finalizers,verbs=update

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradejobhooks,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile reconciles a UpgradeJob object and starts the upgrade if necessary.
func (r *UpgradeJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("UpgradeJobReconciler.Reconcile")
	l.Info("Reconciling UpgradeJob")

	var uj managedupgradev1beta1.UpgradeJob
	if err := r.Get(ctx, req.NamespacedName, &uj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !uj.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	cont, err := r.executeHooks(ctx, &uj, managedupgradev1beta1.EventCreate)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !cont {
		return ctrl.Result{}, nil
	}

	if apimeta.IsStatusConditionTrue(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded) ||
		apimeta.IsStatusConditionTrue(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed) {
		return ctrl.Result{}, r.cleanupLock(ctx, &uj)
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

	// Check if the desired version is already set
	if version.Spec.DesiredUpdate == nil || *version.Spec.DesiredUpdate != uj.Spec.DesiredVersion {
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
		version.Spec.DesiredUpdate = &uj.Spec.DesiredVersion
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

func (r *UpgradeJobReconciler) executeHooks(ctx context.Context, uj *managedupgradev1beta1.UpgradeJob, eventName string) (bool, error) {
	l := log.FromContext(ctx)

	var allHooks managedupgradev1beta1.UpgradeJobHookList
	if err := r.List(ctx, &allHooks, client.InNamespace(uj.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list hooks: %w", err)
	}

	hooks := make([]managedupgradev1beta1.UpgradeJobHook, 0, len(allHooks.Items))
	for _, hook := range allHooks.Items {
		if !slices.Contains(hook.Spec.On, eventName) {
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
		hooks = append(hooks, hook)
	}

	done := true
	errors := []error{}
	for _, hook := range hooks {
		job, err := r.jobForUpgradeJobAndHook(ctx, uj, hook, eventName)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		if !isJobFinished(job) {
			done = false
			continue
		}

		c := jobFailedCondition(job)
		if c != nil && hook.Spec.GetFailurePolicy() == managedupgradev1beta1.FailurePolicyIgnore {
			l.Info("hook failed but failure policy is ignore", "hook", hook.Name, "job", job.Name, "reason", c.Reason, "message", c.Message)
		} else if c != nil {
			r.setStatusCondition(&uj.Status.Conditions, metav1.Condition{
				Type:    managedupgradev1beta1.UpgradeJobConditionFailed,
				Status:  metav1.ConditionTrue,
				Reason:  managedupgradev1beta1.UpgradeJobReasonHookFailed,
				Message: fmt.Sprintf("hook %q failed with %q: %s", hook.Name, c.Reason, c.Message),
			})
			return false, r.Status().Update(ctx, uj)
		}
	}
	if err := multierr.Combine(errors...); err != nil {
		return false, err
	}

	return done, nil
}

func (r *UpgradeJobReconciler) jobForUpgradeJobAndHook(ctx context.Context, uj *managedupgradev1beta1.UpgradeJob, hook managedupgradev1beta1.UpgradeJobHook, eventName string) (batchv1.Job, error) {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(uj.Namespace), client.MatchingLabels(jobLabels(uj.Name, hook.Name, eventName))); err != nil {
		return batchv1.Job{}, err
	}
	if len(jobs.Items) == 0 {
		return r.createHookJob(ctx, hook, uj, eventName)
	}
	if len(jobs.Items) > 1 {
		log.FromContext(ctx).Error(fmt.Errorf("found multiple jobs for upgrade job %q hook %q event %q, ignoring all but first", uj.Name, hook.Name, eventName), "jobs", jobs.Items)
	}
	return jobs.Items[0], nil
}

func (r *UpgradeJobReconciler) createHookJob(ctx context.Context, hook managedupgradev1beta1.UpgradeJobHook, uj *managedupgradev1beta1.UpgradeJob, eventName string) (batchv1.Job, error) {
	ll := make(map[string]string)
	maps.Copy(ll, hook.Spec.Template.Labels)
	maps.Copy(ll, jobLabels(uj.Name, hook.Name, eventName))

	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// TODO(bastjan): always generate valid names
			Name:        strings.ToLower(fmt.Sprintf("%s-%s-%s-hook", uj.Name, hook.Name, eventName)),
			Namespace:   uj.Namespace,
			Annotations: hook.Spec.Template.Annotations,
			Labels:      ll,
			Finalizers:  []string{UpgradeJobHookJobFinalizer},
		},
		Spec: hook.Spec.Template.Spec,
	}

	if err := ctrl.SetControllerReference(uj, &job, r.Scheme); err != nil {
		return batchv1.Job{}, fmt.Errorf("failed to set controller reference: %w", err)
	}

	if err := r.Create(ctx, &job); err != nil {
		return batchv1.Job{}, fmt.Errorf("failed to create hook job: %w", err)
	}

	return job, nil
}

func jobLabels(jobName, hookName, eventName string) map[string]string {
	return map[string]string{
		prefixJobLabel("upgradejobhook"): hookName,
		prefixJobLabel("upgradejob"):     jobName,
		prefixJobLabel("event"):          eventName,
	}
}

func prefixJobLabel(name string) string {
	return fmt.Sprintf("%s/%s", managedupgradev1beta1.GroupVersion.Group, name)
}

// isJobFinished checks whether the given Job has finished execution.
// It does not discriminate between successful and failed terminations.
func isJobFinished(j batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// jobFailedCondition returns the JobCondition with type JobFailed if it exists and is true, nil otherwise.
func jobFailedCondition(j batchv1.Job) *batchv1.JobCondition {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == v1.ConditionTrue {
			return &c
		}
	}
	return nil
}
