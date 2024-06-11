package controllers

import (
	"context"
	"fmt"
	"time"

	"dario.cat/mergo"
	configv1 "github.com/openshift/api/config/v1"
	"golang.org/x/exp/slices"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
)

// ClusterVersionReconciler reconciles a ClusterVersion object
type ClusterVersionReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Clock

	ManagedUpstreamClusterVersionName string
	ManagedClusterVersionName         string
	ManagedClusterVersionNamespace    string
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch

//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=clusterversions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=clusterversions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=clusterversions/finalizers,verbs=update

// Reconcile compares the ClusterVersion object with the upstream ClusterVersion object and updates the upstream ClusterVersion object if necessary.
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	now := r.Clock.Now()
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
		Name: r.ManagedUpstreamClusterVersionName,
	}, &upstreamVersion); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get upstream cluster version: %w", err)
	}

	overlaidSpec, err := applyOverlay(version.Spec.Template, version.Spec.Overlays, now)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply overlay: %w", err)
	}

	if clusterversion.SpecEqualIgnoringDesiredUpdate(overlaidSpec.Spec, upstreamVersion.Spec) {
		return nextRequeue(version, now), nil
	}

	l.Info("updating cluster version", "old", overlaidSpec.Spec, "new", upstreamVersion.Spec)

	du := upstreamVersion.Spec.DesiredUpdate
	upstreamVersion.Spec = overlaidSpec.Spec
	upstreamVersion.Spec.DesiredUpdate = du

	if err := r.Update(ctx, &upstreamVersion); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update cluster version: %w", err)
	}

	return nextRequeue(version, now), nil
}

// Filter filters the ClusterVersion objects to only reconcile the managed ClusterVersion object.
// Returns true if the object is managed by this controller.
func (r *ClusterVersionReconciler) Filter(obj client.Object) bool {
	switch obj.(type) {
	case *managedupgradev1beta1.ClusterVersion:
		return r.ManagedClusterVersionName == obj.GetName() && r.ManagedClusterVersionNamespace == obj.GetNamespace()
	case *configv1.ClusterVersion:
		return r.ManagedUpstreamClusterVersionName == obj.GetName()
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
// The controller is setup to watch the managed ClusterVersion object and the upstream ClusterVersion object.
func (r *ClusterVersionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.ClusterVersion{}).
		Watches(&configv1.ClusterVersion{}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(predicate.NewPredicateFuncs(r.Filter)).
		Complete(r)
}

func applyOverlay(base managedupgradev1beta1.ClusterVersionTemplate, overlays []managedupgradev1beta1.ClusterVersionOverlayConfig, now time.Time) (managedupgradev1beta1.ClusterVersionTemplate, error) {
	b := base.DeepCopy()
	so := slices.Clone(overlays)
	sortOverlaysByFrom(so)
	slices.Reverse(so)

	var o *managedupgradev1beta1.ClusterVersionOverlay
	for _, overlay := range so {
		if now.Before(overlay.From.Time) {
			continue
		}
		o = &overlay.Overlay
		break
	}

	if o == nil {
		return *b, nil
	}
	return *b, mergo.Merge(b, managedupgradev1beta1.ClusterVersionTemplate{Spec: configv1.ClusterVersionSpec(o.Spec)}, mergo.WithOverride)
}

// nextRequeue returns the time until the next overlay is applied.
func nextRequeue(version managedupgradev1beta1.ClusterVersion, now time.Time) ctrl.Result {
	so := slices.Clone(version.Spec.Overlays)
	sortOverlaysByFrom(so)

	for _, overlay := range so {
		if overlay.From.Time.Before(now) || overlay.From.Time.Equal(now) {
			continue
		}
		return ctrl.Result{Requeue: true, RequeueAfter: overlay.From.Sub(now)}
	}

	return ctrl.Result{}
}

// sortOverlaysByFrom sorts the given slice of overlays by the From field.
func sortOverlaysByFrom(os []managedupgradev1beta1.ClusterVersionOverlayConfig) {
	slices.SortFunc(os, func(a, b managedupgradev1beta1.ClusterVersionOverlayConfig) int {
		if a.From.Time.Equal(b.From.Time) {
			return 0
		} else if a.From.Time.Before(b.From.Time) {
			return -1
		}
		return 1
	})
}
