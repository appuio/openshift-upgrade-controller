package controllers

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

// UpgradeSuspensionWindowReconciler reconciles a UpgradeConfig object
type UpgradeSuspensionWindowReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradesuspensionwindows,verbs=get;list;watch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=upgradesuspensionwindows/status,verbs=get;update;patch

// Reconcile implements the reconcile loop for UpgradeSuspensionWindow.
// It writes the list of matching UpgradeConfigs and UpgradeJobs to the status.
func (r *UpgradeSuspensionWindowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("UpgradeSuspensionWindowReconciler.Reconcile")
	l.Info("Reconciling UpgradeSuspensionWindow")

	var usw managedupgradev1beta1.UpgradeSuspensionWindow
	if err := r.Get(ctx, req.NamespacedName, &usw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !usw.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	cSel, err := metav1.LabelSelectorAsSelector(usw.Spec.ConfigSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse config selector: %w", err)
	}
	var configs managedupgradev1beta1.UpgradeConfigList
	if err := r.List(ctx, &configs, client.MatchingLabelsSelector{Selector: cSel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list matching UpgradeConfigs: %w", err)
	}

	jSel, err := metav1.LabelSelectorAsSelector(usw.Spec.JobSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse job selector: %w", err)
	}
	var jobs managedupgradev1beta1.UpgradeJobList
	if err := r.List(ctx, &jobs, client.MatchingLabelsSelector{Selector: jSel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list matching UpgradeJobs: %w", err)
	}

	configNames := make([]managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject, len(configs.Items))
	for i, config := range configs.Items {
		configNames[i] = managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject{Name: config.Name}
	}
	slices.SortFunc(configNames, func(a, b managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject) int {
		return strings.Compare(a.Name, b.Name)
	})
	usw.Status.MatchingConfigs = configNames

	jobNames := make([]managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject, 0, len(jobs.Items))
	for _, job := range jobs.Items {
		if !job.Spec.StartAfter.Time.Before(usw.Spec.Start.Time) && !job.Spec.StartAfter.Time.After(usw.Spec.End.Time) {
			jobNames = append(jobNames, managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject{Name: job.Name})
		}
	}
	slices.SortFunc(jobNames, func(a, b managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject) int {
		return strings.Compare(a.Name, b.Name)
	})
	usw.Status.MatchingJobs = jobNames

	if err := r.Status().Update(ctx, &usw); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UpgradeSuspensionWindowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.UpgradeSuspensionWindow{}).
		Complete(r)
}
