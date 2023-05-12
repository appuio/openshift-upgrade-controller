package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/robfig/cron/v3"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
)

type Clock interface {
	Now() time.Time
}

// UpgradeConfigReconciler reconciles a UpgradeConfig object
type UpgradeConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Clock Clock

	ManagedUpstreamClusterVersionName string
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs/finalizers,verbs=update

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

	// Schedule is suspended, do nothing
	if uc.Spec.Schedule.Suspend {
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

	jobs, err := r.getControlledJobs(ctx, uc)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not get controlled jobs: %w", err)
	}
	latestJob := latestScheduledJob(jobs)
	now := r.Clock.Now().In(location)
	earliestTimestamp := uc.ObjectMeta.CreationTimestamp.Time

	if latestJob != nil {
		// if there is a future job scheduled, do nothing
		if latestJob.Spec.StartAfter.After(now) {
			return ctrl.Result{}, nil
		}
		// if the latest job is not completed, do nothing
		isCompleted := apimeta.IsStatusConditionTrue(latestJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded) ||
			apimeta.IsStatusConditionTrue(latestJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
		if !isCompleted {
			return ctrl.Result{}, nil
		}

		earliestTimestamp = latestJob.Spec.StartAfter.Time
	}

	sched, err := cron.ParseStandard(uc.Spec.Schedule.Cron)
	if err != nil {
		return ctrl.Result{}, err
	}

	nextRun, err := calcNextRun(earliestTimestamp.In(location), sched, uc.Spec.Schedule.IsoWeek)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not find next run: %w", err)
	}
	nextCreateJobWindow := nextRun.Add(-uc.Spec.PinVersionWindow.Duration)

	// check if we are in a scheduling window
	// if we are not yet in the scheduling window, requeue until we are
	if now.Before(nextCreateJobWindow) {
		return ctrl.Result{RequeueAfter: nextCreateJobWindow.Sub(now)}, nil
	}
	// if we are past the scheduling window, do nothing
	if now.After(nextCreateJobWindow.Add(uc.Spec.MaxSchedulingDelay.Duration)) {
		return ctrl.Result{}, nil
	}

	var cv configv1.ClusterVersion
	if err := r.Client.Get(ctx, types.NamespacedName{Name: r.ManagedUpstreamClusterVersionName}, &cv); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not get cluster version: %w", err)
	}

	latestUpdate := clusterversion.LatestAvailableUpdate(cv)
	if latestUpdate == nil {
		l.Info("no updates available")
		return ctrl.Result{}, nil
	}

	return r.createJob(uc, *latestUpdate, nextRun, ctx)
}

func (r *UpgradeConfigReconciler) createJob(uc managedupgradev1beta1.UpgradeConfig, latestUpdate configv1.Release, nextRun time.Time, ctx context.Context) (reconcile.Result, error) {
	newJob := managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      uc.Name + "-" + strings.ReplaceAll(latestUpdate.Version, ".", "-") + "-" + strconv.FormatInt(nextRun.Unix(), 10),
			Namespace: uc.Namespace,
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter:  metav1.NewTime(nextRun),
			StartBefore: metav1.NewTime(nextRun.Add(uc.Spec.MaxUpgradeStartDelay.Duration)),

			DesiredVersion: configv1.Update{
				Version: latestUpdate.Version,
				Image:   latestUpdate.Image,
			},

			UpgradeJobConfig: uc.Spec.JobTemplate.Spec.Config,
		},
	}

	if err := ctrl.SetControllerReference(&uc, &newJob, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not set controller reference: %w", err)
	}

	if err := r.Create(ctx, &newJob); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not create job: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UpgradeConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.UpgradeConfig{}).
		Complete(r)
}

func (r *UpgradeConfigReconciler) getControlledJobs(ctx context.Context, uc managedupgradev1beta1.UpgradeConfig) ([]managedupgradev1beta1.UpgradeJob, error) {
	var jobs managedupgradev1beta1.UpgradeJobList
	if err := r.List(ctx, &jobs, client.InNamespace(uc.Namespace)); err != nil {
		return nil, err
	}
	return filterControlledJobs(uc, jobs.Items), nil
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

func filterControlledJobs(uc managedupgradev1beta1.UpgradeConfig, jobs []managedupgradev1beta1.UpgradeJob) []managedupgradev1beta1.UpgradeJob {
	ownedJobs := make([]managedupgradev1beta1.UpgradeJob, 0, len(jobs))
	for _, job := range jobs {
		if metav1.IsControlledBy(&job, &uc) {
			ownedJobs = append(ownedJobs, job)
		}
	}
	return ownedJobs
}

func calcNextRun(earliest time.Time, sched cron.Schedule, schedISOWeek string) (time.Time, error) {
	nextRun := sched.Next(earliest)
	for i := 0; i < 1000; i++ {
		isoWeekOK, err := checkIsoWeek(nextRun, schedISOWeek)
		if err != nil {
			return time.Time{}, err
		}
		if isoWeekOK {
			return nextRun, nil
		}
		nextRun = sched.Next(nextRun)
	}
	return time.Time{}, fmt.Errorf("could not find next run, max time: %s", nextRun)
}

// checkIsoWeek checks if the given time is in the given iso week.
// The iso week can be one of the following:
// - "": every iso week
// - "@even": every even iso week
// - "@odd": every odd iso week
// - "<N>": every iso week N
func checkIsoWeek(t time.Time, schedISOWeek string) (bool, error) {
	_, iw := t.ISOWeek()
	switch schedISOWeek {
	case "":
		return true, nil
	case "@even":
		return iw%2 == 0, nil
	case "@odd":
		return iw%2 == 1, nil
	case strconv.Itoa(iw):
		return true, nil
	default:
		return false, fmt.Errorf("unknown iso week: %s", schedISOWeek)
	}
}
