package controllers

import (
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_ClusterVersionReconciler_Reconcile(t *testing.T) {
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}
	ctx := log.IntoContext(t.Context(), testr.New(t))

	upstream := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "9b588658-9671-429c-a762-34106da5795f",
			DesiredUpdate: &configv1.Update{
				Version: "4.5.89",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d732fee6462de7f04f9432f1bb3925f57554db1d8c8d6f3138eea70e5787c7ae",
			},
			Upstream: "https://api.openshift.com/api/upgrades_info/v1/graph",
			Channel:  "stable-4.5",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet: "v4.5",
			},
		},
	}

	newBaseClusterVersionSpec := func() configv1.ClusterVersionSpec {
		return configv1.ClusterVersionSpec{
			ClusterID: "9b588658-9671-429c-a762-34106da5795f",
			DesiredUpdate: &configv1.Update{
				Version: "ignored",
			},
			Upstream: "https://api.openshift.com/api/upgrades_info/v1/graph",
			Channel:  "stable-4.6",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet: "v4.6",
			},
		}
	}

	managed := &managedupgradev1beta1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "version",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.ClusterVersionSpec{
			Template: managedupgradev1beta1.ClusterVersionTemplate{
				Spec: newBaseClusterVersionSpec(),
			},
			Overlays: []managedupgradev1beta1.ClusterVersionOverlayConfig{
				{
					From: metav1.NewTime(clock.Now().Add(150 * time.Minute)),
					Overlay: managedupgradev1beta1.ClusterVersionOverlay{
						Spec: managedupgradev1beta1.ConfigV1ClusterVersionSpec{
							Channel: "stable-4.8",
						},
					},
				},
				{
					From: metav1.NewTime(clock.Now().Add(time.Hour)),
					Overlay: managedupgradev1beta1.ClusterVersionOverlay{
						Spec: managedupgradev1beta1.ConfigV1ClusterVersionSpec{
							Channel: "stable-4.7",
						},
					},
				},
			},
		},
	}

	cli := controllerClient(t, upstream, managed)

	subject := &ClusterVersionReconciler{
		Client: cli,
		Scheme: cli.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
		ManagedClusterVersionName:         "version",
		ManagedClusterVersionNamespace:    "appuio-openshift-upgrade-controller",
	}

	ret, err := subject.Reconcile(ctx, reconcile.Request{})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{Requeue: true, RequeueAfter: time.Hour}, ret, "should requeue when new overlay should be applied")

	updatedUpstream := &configv1.ClusterVersion{}
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "version"}, updatedUpstream))

	require.Equal(t, upstream.Spec.DesiredUpdate, updatedUpstream.Spec.DesiredUpdate, "DesiredUpdate should not be managed")

	managed.Spec.Template.Spec.DesiredUpdate = nil
	updatedUpstream.Spec.DesiredUpdate = nil
	require.Equal(t, managed.Spec.Template.Spec, updatedUpstream.Spec, "Spec should be updated to match managed template")

	clock.Advance(ret.RequeueAfter)
	ret2, err := subject.Reconcile(ctx, reconcile.Request{})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{Requeue: true, RequeueAfter: (150 * time.Minute) - ret.RequeueAfter}, ret2, "should requeue when new overlay should be applied")
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "version"}, updatedUpstream))
	require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(managed), managed))
	require.Equal(t, managed.Status.OverlayApplied, managed.Spec.Overlays[1].From, "should update OverlayApplied")
	managed.Spec.Template.Spec.Channel = "stable-4.7"
	managed.Spec.Template.Spec.DesiredUpdate = nil
	updatedUpstream.Spec.DesiredUpdate = nil
	require.Equal(t, managed.Spec.Template.Spec, updatedUpstream.Spec, "should apply first overlay")

	clock.Advance(12 * time.Minute)
	ret3, err := subject.Reconcile(ctx, reconcile.Request{})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{Requeue: true, RequeueAfter: (150 * time.Minute) - (ret.RequeueAfter + 12*time.Minute)}, ret3, "should requeue when new overlay should be applied")
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "version"}, updatedUpstream))
	require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(managed), managed))
	require.Equal(t, managed.Status.OverlayApplied, managed.Spec.Overlays[1].From, "should update OverlayApplied")
	managed.Spec.Template.Spec.Channel = "stable-4.7"
	managed.Spec.Template.Spec.DesiredUpdate = nil
	updatedUpstream.Spec.DesiredUpdate = nil
	require.Equal(t, managed.Spec.Template.Spec, updatedUpstream.Spec, "first overlay should stay applied")

	clock.Advance(ret3.RequeueAfter)
	ret4, err := subject.Reconcile(ctx, reconcile.Request{})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, ret4, "no more overlays to apply")
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "version"}, updatedUpstream))
	require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(managed), managed))
	require.Equal(t, managed.Status.OverlayApplied, managed.Spec.Overlays[0].From, "should update OverlayApplied")
	managed.Spec.Template.Spec.Channel = "stable-4.8"
	managed.Spec.Template.Spec.DesiredUpdate = nil
	updatedUpstream.Spec.DesiredUpdate = nil
	require.Equal(t, managed.Spec.Template.Spec, updatedUpstream.Spec, "should apply second overlay")

	clock.Advance(7 * time.Hour)
	ret5, err := subject.Reconcile(ctx, reconcile.Request{})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, ret5, "no more overlays to apply")
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "version"}, updatedUpstream))
	require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(managed), managed))
	require.Equal(t, managed.Status.OverlayApplied, managed.Spec.Overlays[0].From, "should update OverlayApplied")
	managed.Spec.Template.Spec.Channel = "stable-4.8"
	managed.Spec.Template.Spec.DesiredUpdate = nil
	updatedUpstream.Spec.DesiredUpdate = nil
	require.Equal(t, managed.Spec.Template.Spec, updatedUpstream.Spec, "second overlay should stay applied")

	var expectedPreviews []managedupgradev1beta1.ClusterVersionStatusOverlays
	for _, overlay := range managed.Spec.Overlays {
		b := newBaseClusterVersionSpec()
		b.Channel = overlay.Overlay.Spec.Channel
		expectedPreviews = append(expectedPreviews, managedupgradev1beta1.ClusterVersionStatusOverlays{
			From: overlay.From,
			Preview: managedupgradev1beta1.ClusterVersionStatusPreview{
				Spec: b,
			},
		})
	}
	require.Equal(t, expectedPreviews, managed.Status.Overlays, "should keep previews up-to-date")
}

func Test_ClusterVersionReconciler_Filter(t *testing.T) {
	subject := &ClusterVersionReconciler{
		ManagedUpstreamClusterVersionName: "version",
		ManagedClusterVersionName:         "version",
		ManagedClusterVersionNamespace:    "appuio-openshift-upgrade-controller",
	}

	require.True(t, subject.Filter(&configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}))
	require.True(t, subject.Filter(&managedupgradev1beta1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "version",
			Namespace: "appuio-openshift-upgrade-controller",
		},
	}))
	require.False(t, subject.Filter(&corev1.Pod{}), "should filter unknown types")
}
