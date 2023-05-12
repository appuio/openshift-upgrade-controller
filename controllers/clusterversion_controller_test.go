package controllers

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_ClusterVersionReconciler_Reconcile(t *testing.T) {
	ctx := context.Background()

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

	managed := &managedupgradev1beta1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "version",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.ClusterVersionSpec{
			Template: managedupgradev1beta1.ClusterVersionTemplate{
				Spec: configv1.ClusterVersionSpec{
					ClusterID: "9b588658-9671-429c-a762-34106da5795f",
					DesiredUpdate: &configv1.Update{
						Version: "ignored",
					},
					Upstream: "https://api.openshift.com/api/upgrades_info/v1/graph",
					Channel:  "stable-4.6",
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet: "v4.6",
					},
				},
			},
		},
	}

	client := controllerClient(t, upstream, managed)

	subject := &ClusterVersionReconciler{
		Client: client,
		Scheme: client.Scheme(),

		ManagedUpstreamClusterVersionName: "version",
		ManagedClusterVersionName:         "version",
		ManagedClusterVersionNamespace:    "appuio-openshift-upgrade-controller",
	}

	_, err := subject.Reconcile(ctx, reconcile.Request{})
	require.NoError(t, err)

	updatedUpstream := &configv1.ClusterVersion{}
	err = client.Get(ctx, types.NamespacedName{Name: "version"}, updatedUpstream)
	require.NoError(t, err)

	require.Equal(t, upstream.Spec.DesiredUpdate, updatedUpstream.Spec.DesiredUpdate, "DesiredUpdate should not be managed")

	managed.Spec.Template.Spec.DesiredUpdate = nil
	updatedUpstream.Spec.DesiredUpdate = nil
	require.Equal(t, managed.Spec.Template.Spec, updatedUpstream.Spec, "Spec should be updated to match managed template")
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
