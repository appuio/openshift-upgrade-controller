package controllers

import (
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_ClusterVersionCollector(t *testing.T) {
	expectedMetricNames := []string{
		"openshift_upgrade_controller_cluster_version_info",
		"openshift_upgrade_controller_cluster_version_overlay_timestamp_seconds",
	}

	version := &managedupgradev1beta1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "version",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Status: managedupgradev1beta1.ClusterVersionStatus{
			Current: configv1.ClusterVersionSpec{
				ClusterID: "9b588658-9671-429c-a762-34106da5795f",
				Channel:   "stable-4.5",
				Upstream:  "https://api.openshift.com/api/upgrades_info/v1/graph",
			},
			Overlays: []managedupgradev1beta1.ClusterVersionStatusOverlays{
				{
					From: metav1.NewTime(time.Date(2022, 12, 4, 14, 0, 0, 0, time.UTC)),
					Preview: managedupgradev1beta1.ClusterVersionStatusPreview{
						Spec: configv1.ClusterVersionSpec{
							ClusterID: "9b588658-9671-429c-a762-34106da5795f",
							Channel:   "stable-4.6",
							Upstream:  "https://api.openshift.com/api/upgrades_info/v1/graph",
							Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
								BaselineCapabilitySet: "v4.6",
							},
						},
					},
				},
			},
		},
	}

	c := controllerClient(t, version)
	subject := &ClusterVersionCollector{
		Client: c,

		ManagedClusterVersionName:      version.Name,
		ManagedClusterVersionNamespace: version.Namespace,
	}

	require.NoError(t,
		testutil.CollectAndCompare(subject, strings.NewReader(`# HELP openshift_upgrade_controller_cluster_version_info Managed ClusterVersion info metric. Shows the currently applied cluster version.
# TYPE openshift_upgrade_controller_cluster_version_info gauge
openshift_upgrade_controller_cluster_version_info{baseline_capability_set="",channel="stable-4.5",cluster_id="9b588658-9671-429c-a762-34106da5795f",cluster_version="version",upstream="https://api.openshift.com/api/upgrades_info/v1/graph"} 1
# HELP openshift_upgrade_controller_cluster_version_overlay_timestamp_seconds Managed ClusterVersion info metric. Shows the fully merged cluster versions applied at their respective timestamps. The value is the timestamp in seconds since epoch.
# TYPE openshift_upgrade_controller_cluster_version_overlay_timestamp_seconds gauge
openshift_upgrade_controller_cluster_version_overlay_timestamp_seconds{baseline_capability_set="v4.6",channel="stable-4.6",cluster_id="9b588658-9671-429c-a762-34106da5795f",cluster_version="version",from="2022-12-04T14:00:00Z",upstream="https://api.openshift.com/api/upgrades_info/v1/graph"} 1.6701624e+09
`), expectedMetricNames...))
}

func Test_ClusterVersionCollector_NotFound(t *testing.T) {
	c := controllerClient(t)

	subject := &ClusterVersionCollector{
		Client: c,

		ManagedClusterVersionName:      "version",
		ManagedClusterVersionNamespace: "appuio-openshift-upgrade-controller",
	}

	require.ErrorContains(t, testutil.CollectAndCompare(subject, strings.NewReader(""), "openshift_upgrade_controller_cluster_version_info"), "not found")
}
