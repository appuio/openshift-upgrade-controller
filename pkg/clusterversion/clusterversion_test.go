package clusterversion_test

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
)

func TestCompareSpecIgnoringDesiredUpdate(t *testing.T) {
	testCases := []struct {
		name  string
		a, b  configv1.ClusterVersionSpec
		equal bool
	}{
		{
			name: "a and b are equal",
			a: configv1.ClusterVersionSpec{
				ClusterID: "1234",
				Upstream:  "https://api.openshift.com/api/upgrades_info/v1/graph",
				Channel:   "release-4.5",
				Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
					BaselineCapabilitySet:         "v4.3",
					AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{"bonk"},
				},
			},
			b: configv1.ClusterVersionSpec{
				ClusterID: "1234",
				Upstream:  "https://api.openshift.com/api/upgrades_info/v1/graph",
				Channel:   "release-4.5",
				Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
					BaselineCapabilitySet:         "v4.3",
					AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{"bonk"},
				},
			},
			equal: true,
		},
		{
			name: "a and b are not equal",
			a: configv1.ClusterVersionSpec{
				ClusterID: "1234",
			},
			b: configv1.ClusterVersionSpec{
				ClusterID: "1234",
				Overrides: []configv1.ComponentOverride{{Name: "new override"}},
			},
			equal: false,
		},
		{
			name: "desired update is ignored",
			a: configv1.ClusterVersionSpec{
				ClusterID: "1234",
				DesiredUpdate: &configv1.Update{
					Version: "1.2.3",
				},
			},
			b: configv1.ClusterVersionSpec{
				ClusterID: "1234",
				DesiredUpdate: &configv1.Update{
					Version: "1.2.4",
				},
			},
			equal: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			equal := clusterversion.SpecEqualIgnoringDesiredUpdate(tc.a, tc.b)
			if equal != tc.equal {
				t.Errorf("expected %t, got %t", tc.equal, equal)
			}
		})
	}
}
