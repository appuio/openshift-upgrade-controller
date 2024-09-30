package controllers

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/robfig/cron/v3"
	"go.uber.org/multierr"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
	"github.com/appuio/openshift-upgrade-controller/pkg/schedule"
)

const (
	EventReasonUpgradeConfigSuspended                   = "UpgradeConfigSuspended"
	EventReasonUpgradeConfigSuspendedBySuspensionWindow = "UpgradeConfigSuspendedBySuspensionWindow"
)

type Clock interface {
	Now() time.Time
}

// UpgradeConfigReconciler reconciles a UpgradeConfig object
type UpgradeConfigReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Clock Clock

	ManagedUpstreamClusterVersionName string
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs/finalizers,verbs=update

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradesuspensionwindows,verbs=get;list;watch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradesuspensionwindows/status,verbs=get

// Reconcile implements the reconcile loop for UpgradeConfig.
// It schedules UpgradeJobs based on the UpgradeConfig's schedule - if an update is available.
func (r *UpgradeConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("UpgradeConfigReconciler.Reconcile")
	l.Info("Reconciling UpgradeConfig")

	var uc managedupgradev1beta1.UpgradeConfig
	if err := r.Get(ctx, req.NamespacedName, &uc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !uc.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	location := time.Local
	if uc.Spec.Schedule.Location != "" {
		l, err := time.LoadLocation(uc.Spec.Schedule.Location)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("could not load location: %w", err)
		}
		location = l
	}

	s, err := cron.ParseStandard(uc.Spec.Schedule.Cron)
	if err != nil {
		return ctrl.Result{}, err
	}
	sched := schedule.Schedule{Schedule: s, IsoWeek: uc.Spec.Schedule.IsoWeek}

	jobSchedRequeue, schedErr := r.scheduleJob(ctx, &uc, sched, location)
	statusRequeue, stErr := r.updateNextPossibleSchedulesStatus(ctx, &uc, sched, location)
	if err := multierr.Append(schedErr, stErr); err != nil {
		return ctrl.Result{}, err
	}

	nextRequeue := time.Duration(math.MaxInt64)
	if jobSchedRequeue > 0 && jobSchedRequeue < nextRequeue {
		nextRequeue = jobSchedRequeue
	}
	if statusRequeue > 0 && statusRequeue < nextRequeue {
		nextRequeue = statusRequeue
	}
	// ensure we always requeue after a minute, if no requeue set, so we don't miss the next run on some corner cases
	if nextRequeue == math.MaxInt64 {
		nextRequeue = time.Minute
	}

	return ctrl.Result{RequeueAfter: nextRequeue}, nil
}

func (r *UpgradeConfigReconciler) scheduleJob(ctx context.Context, uc *managedupgradev1beta1.UpgradeConfig, sched schedule.Schedule, location *time.Location) (time.Duration, error) {
	l := log.FromContext(ctx).WithName("UpgradeConfigReconciler.scheduleJob")

	jobs, err := r.getControlledJobs(ctx, uc)
	if err != nil {
		return 0, fmt.Errorf("could not get controlled jobs: %w", err)
	}
	latestJob := latestScheduledJob(jobs)
	now := r.Clock.Now().In(location)
	earliestTimestamp := uc.ObjectMeta.CreationTimestamp.Time
	if uc.Status.LastScheduledUpgrade != nil {
		earliestTimestamp = uc.Status.LastScheduledUpgrade.Time
	}

	if latestJob != nil {
		// status might have failed to update, try again
		if uc.Status.LastScheduledUpgrade == nil || latestJob.Spec.StartAfter.After(uc.Status.LastScheduledUpgrade.Time) {
			return 0, r.setLastScheduledUpgrade(ctx, uc, latestJob.Spec.StartAfter.Time)
		}

		// if there is a future job scheduled, do nothing
		if latestJob.Spec.StartAfter.After(now) {
			l.Info("future job already scheduled", "job", latestJob.Name, "startAfter", latestJob.Spec.StartAfter.Time)
			return 0, nil
		}
		// if the latest job is not completed, do nothing
		isCompleted := apimeta.IsStatusConditionTrue(latestJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded) ||
			apimeta.IsStatusConditionTrue(latestJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
		if !isCompleted {
			l.Info("latest job not completed", "job", latestJob.Name)
			return 0, nil
		}

		earliestTimestamp = latestJob.Spec.StartAfter.Time
	}

	nextRun := earliestTimestamp.In(location)
	nextRunAttempts := 0
findNextRun:
	nextRunAttempts++
	nextRun, err = sched.Next(nextRun)
	if err != nil {
		return 0, fmt.Errorf("could not find next run: %w", err)
	}
	nextCreateJobWindow := nextRun.Add(-uc.Spec.PinVersionWindow.Duration)

	// check if we are in a scheduling window
	// if we are not yet in the scheduling window, requeue until we are
	if now.Before(nextCreateJobWindow) {
		l.Info("not yet in scheduling window", "window", nextCreateJobWindow)
		return nextCreateJobWindow.Sub(now), nil
	}
	// find next scheduling window if we're past the current one
	if now.After(nextCreateJobWindow.Add(uc.Spec.MaxSchedulingDelay.Duration)) {
		if nextRunAttempts > 100 {
			return 0, fmt.Errorf("could not find next scheduling window after %d attempts. Most likely missed too many schedules", nextRunAttempts)
		}
		goto findNextRun
	}

	var cv configv1.ClusterVersion
	if err := r.Client.Get(ctx, types.NamespacedName{Name: r.ManagedUpstreamClusterVersionName}, &cv); err != nil {
		return 0, fmt.Errorf("could not get cluster version: %w", err)
	}

	// Schedule is suspended, do nothing
	if uc.Spec.Schedule.Suspend {
		l.Info("would schedule job, but schedule is suspended by .spec.schedule.suspend")
		r.Recorder.Event(uc, "Normal", EventReasonUpgradeConfigSuspended, "Upgrade scheduling is suspended by .spec.schedule.suspend")
		return 0, r.setLastScheduledUpgrade(ctx, uc, nextRun)
	}
	// Check if we are in a suspension window
	window, err := r.matchingUpgradeSuspensionWindow(ctx, *uc, now)
	if err != nil {
		return 0, fmt.Errorf("could not search matching upgrade suspension window: %w", err)
	}
	if window != nil {
		l.Info("would schedule job, but schedule is suspended by UpgradeSuspensionWindow", "window", window.Name, "reason", window.Spec.Reason, "start", window.Spec.Start.Time, "end", window.Spec.End.Time)
		r.Recorder.Eventf(uc, "Normal", EventReasonUpgradeConfigSuspendedBySuspensionWindow, "Upgrade scheduling is suspended by UpgradeSuspensionWindow %s: %s", window.Name, window.Spec.Reason)
		return 0, r.setLastScheduledUpgrade(ctx, uc, nextRun)
	}

	latestUpdate := clusterversion.LatestAvailableUpdate(cv)
	if err := r.createJob(*uc, latestUpdate, nextRun, ctx); err != nil {
		return 0, fmt.Errorf("could not create job: %w", err)
	}

	return 0, r.setLastScheduledUpgrade(ctx, uc, nextRun)
}

func (r *UpgradeConfigReconciler) setLastScheduledUpgrade(ctx context.Context, uc *managedupgradev1beta1.UpgradeConfig, t time.Time) error {
	uc.Status.LastScheduledUpgrade = &metav1.Time{Time: t}
	return r.Status().Update(ctx, uc)
}

func (r *UpgradeConfigReconciler) createJob(uc managedupgradev1beta1.UpgradeConfig, latestUpdate *configv1.Release, nextRun time.Time, ctx context.Context) error {
	var dv *configv1.Update
	vn := "noop-"
	if latestUpdate != nil {
		dv = &configv1.Update{
			Version: latestUpdate.Version,
			Image:   latestUpdate.Image,
		}
		vn = strings.ReplaceAll(latestUpdate.Version, ".", "-") + "-"
	}

	newJob := managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:        uc.Name + "-" + vn + strconv.FormatInt(nextRun.Unix(), 10),
			Namespace:   uc.Namespace,
			Annotations: uc.Spec.JobTemplate.Metadata.GetAnnotations(),
			Labels:      uc.Spec.JobTemplate.Metadata.GetLabels(),
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter:  metav1.NewTime(nextRun),
			StartBefore: metav1.NewTime(nextRun.Add(uc.Spec.MaxUpgradeStartDelay.Duration)),

			DesiredVersion: dv,

			UpgradeJobConfig: uc.Spec.JobTemplate.Spec.Config,
		},
	}

	if err := ctrl.SetControllerReference(&uc, &newJob, r.Scheme); err != nil {
		return fmt.Errorf("could not set controller reference: %w", err)
	}

	if err := r.Create(ctx, &newJob); err != nil {
		return fmt.Errorf("could not create job: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UpgradeConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.UpgradeConfig{}).
		Owns(&managedupgradev1beta1.UpgradeJob{}).
		Complete(r)
}

func (r *UpgradeConfigReconciler) getControlledJobs(ctx context.Context, uc *managedupgradev1beta1.UpgradeConfig) ([]managedupgradev1beta1.UpgradeJob, error) {
	var jobs managedupgradev1beta1.UpgradeJobList
	if err := r.List(ctx, &jobs, client.InNamespace(uc.Namespace)); err != nil {
		return nil, err
	}
	return filterControlledJobs(uc, jobs.Items), nil
}

// matchingUpgradeSuspensionWindow returns the UpgradeSuspensionWindow that matches the UpgradeConfig and time or nil if none matches.
func (r *UpgradeConfigReconciler) matchingUpgradeSuspensionWindow(ctx context.Context, ucw managedupgradev1beta1.UpgradeConfig, t time.Time) (*managedupgradev1beta1.UpgradeSuspensionWindow, error) {
	l := log.FromContext(ctx)

	var allWindows managedupgradev1beta1.UpgradeSuspensionWindowList
	if err := r.List(ctx, &allWindows, client.InNamespace(ucw.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list hooks: %w", err)
	}
	for _, window := range allWindows.Items {
		sel, err := metav1.LabelSelectorAsSelector(window.Spec.ConfigSelector)
		if err != nil {
			l.Error(err, "failed to parse selector from window", "window", window.Name, "selector", "configSelector")
			continue
		}
		if !sel.Matches(labels.Set(ucw.Labels)) {
			continue
		}
		if t.Before(window.Spec.Start.Time) || t.After(window.Spec.End.Time) {
			continue
		}
		return &window, nil
	}

	return nil, nil
}

func (r *UpgradeConfigReconciler) updateNextPossibleSchedulesStatus(ctx context.Context, uc *managedupgradev1beta1.UpgradeConfig, sched schedule.Schedule, location *time.Location) (time.Duration, error) {
	now := r.Clock.Now().In(location)
	np, err := sched.NextN(now, 10)
	if err != nil && len(np) > 0 {
		log.FromContext(ctx).Error(err, "could not get all possible next schedules", "n_schedules", len(np))
	} else if err != nil {
		return 0, err
	}

	uc.Status.NextPossibleSchedules = make([]managedupgradev1beta1.NextPossibleSchedule, len(np))
	for i, t := range np {
		uc.Status.NextPossibleSchedules[i] = managedupgradev1beta1.NextPossibleSchedule{
			Time: metav1.NewTime(t),
		}
	}

	return np[0].Sub(now), r.Status().Update(ctx, uc)
}

func latestScheduledJob(jobs []managedupgradev1beta1.UpgradeJob) *managedupgradev1beta1.UpgradeJob {
	var latest *managedupgradev1beta1.UpgradeJob
	for _, job := range jobs {
		if latest == nil || job.Spec.StartAfter.Time.After(latest.Spec.StartAfter.Time) {
			latest = &job
		}
	}
	return latest
}

func filterControlledJobs(uc *managedupgradev1beta1.UpgradeConfig, jobs []managedupgradev1beta1.UpgradeJob) []managedupgradev1beta1.UpgradeJob {
	ownedJobs := make([]managedupgradev1beta1.UpgradeJob, 0, len(jobs))
	for _, job := range jobs {
		if metav1.IsControlledBy(&job, uc) {
			ownedJobs = append(ownedJobs, job)
		}
	}
	return ownedJobs
}
