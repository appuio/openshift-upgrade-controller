package controllers

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
)

// ClusterVersionReconciler reconciles a ClusterVersion object
type ClusterVersionReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	ManagedUpstreamClusterVersionName      string
	ManagedUpstreamClusterVersionNamespace string
	ManagedClusterVersionName              string
	ManagedClusterVersionNamespace         string
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=clusterversions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=clusterversions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=clusterversions/finalizers,verbs=update

// Reconcile compares the ClusterVersion object with the upstream ClusterVersion object and updates the upstream ClusterVersion object if necessary.
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("ClusterVersionReconciler.Reconcile")
	l.Info("Reconciling ClusterVersion")

	var version managedupgradev1beta1.ClusterVersion
	if err := r.Get(ctx, types.NamespacedName{
		Name:      r.ManagedClusterVersionName,
		Namespace: r.ManagedClusterVersionNamespace,
	}, &version); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !version.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var upstreamVersion configv1.ClusterVersion
	if err := r.Get(ctx, types.NamespacedName{
		Name:      r.ManagedUpstreamClusterVersionName,
		Namespace: r.ManagedUpstreamClusterVersionNamespace,
	}, &upstreamVersion); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get upstream cluster version: %w", err)
	}

	if clusterversion.SpecEqualIgnoringDesiredUpdate(version.Spec.Template.Spec, upstreamVersion.Spec) {
		return ctrl.Result{}, nil
	}

	l.Info("updating cluster version", "old", version.Spec.Template.Spec, "new", upstreamVersion.Spec)

	du := upstreamVersion.Spec.DesiredUpdate
	upstreamVersion.Spec = version.Spec.Template.Spec
	upstreamVersion.Spec.DesiredUpdate = du

	if err := r.Update(ctx, &upstreamVersion); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update cluster version: %w", err)
	}

	return ctrl.Result{}, nil
}

// Filter filters the ClusterVersion objects to only reconcile the managed ClusterVersion object.
// Returns true if the object is managed by this controller.
func (r *ClusterVersionReconciler) Filter(obj client.Object) bool {
	switch obj.(type) {
	case *managedupgradev1beta1.ClusterVersion:
		return r.ManagedClusterVersionName == obj.GetName() && r.ManagedClusterVersionNamespace == obj.GetNamespace()
	case *configv1.ClusterVersion:
		return r.ManagedUpstreamClusterVersionName == obj.GetName() && r.ManagedUpstreamClusterVersionNamespace == obj.GetNamespace()
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
// The controller is setup to watch the managed ClusterVersion object and the upstream ClusterVersion object.
func (r *ClusterVersionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.ClusterVersion{}).
		Watches(&source.Kind{Type: &configv1.ClusterVersion{}}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(predicate.NewPredicateFuncs(r.Filter)).
		Complete(r)
}
