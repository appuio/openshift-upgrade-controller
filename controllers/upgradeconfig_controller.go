package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

type Clock interface {
	Now() time.Time
}

// UpgradeConfigReconciler reconciles a UpgradeConfig object
type UpgradeConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Clock Clock
}

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradeconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the UpgradeConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *UpgradeConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

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

	sched, err := cron.ParseStandard(uc.Spec.Schedule.Cron)
	if err != nil {
		return ctrl.Result{}, err
	}

	earliestTimestamp := uc.ObjectMeta.CreationTimestamp.Time
	if uc.Status.LastScheduledTime != nil {
		earliestTimestamp = uc.Status.LastScheduledTime.Time
	}

	nextRun, err := calcNextRun(earliestTimestamp, sched, uc.Spec.Schedule.IsoWeek)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not find next run: %w", err)
	}

	_ = nextRun
	// now := r.Clock.Now()
	// withRequeue := ctrl.Result{RequeueAfter: nextRun.Sub(r.Clock.Now())}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UpgradeConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.UpgradeConfig{}).
		Complete(r)
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
