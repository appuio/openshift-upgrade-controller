package clusterversion_test

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"

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

func TestIsVersionUpgradeCompleted(t *testing.T) {
	desiredImg := "quay.io/openshift-release-dev/ocp-release@sha256:1234"
	desiredVer := "4.5.23"

	subject := configv1.ClusterVersion{}
	assert.True(t, clusterversion.IsVersionUpgradeCompleted(subject), "fresh install, desired update not set")

	subject.Spec.DesiredUpdate = &configv1.Update{
		Image:   desiredImg,
		Version: desiredVer,
	}
	assert.False(t, clusterversion.IsVersionUpgradeCompleted(subject), "update not in history")

	subject.Status.History = []configv1.UpdateHistory{
		{
			State:   configv1.CompletedUpdate,
			Image:   "otherimg",
			Version: "otherver",
		},
	}
	assert.False(t, clusterversion.IsVersionUpgradeCompleted(subject), "update not in history")

	subject.Status.History = append(subject.Status.History, configv1.UpdateHistory{
		State:   configv1.PartialUpdate,
		Image:   desiredImg,
		Version: desiredVer,
	})
	assert.False(t, clusterversion.IsVersionUpgradeCompleted(subject), "update in history but not completed")

	subject.Status.History[1].State = configv1.CompletedUpdate
	assert.True(t, clusterversion.IsVersionUpgradeCompleted(subject), "update in history and completed")
}

func TestFindAvailableUpdate(t *testing.T) {
	desiredImg := "quay.io/openshift-release-dev/ocp-release@sha256:1234"
	desiredVer := "4.5.23"

	subject := configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			AvailableUpdates: []configv1.Release{
				{
					Image:   "otherimg",
					Version: "otherver",
				},
				{
					Image:   desiredImg,
					Version: desiredVer,
				},
			},
		},
	}

	assert.Nil(t, clusterversion.FindAvailableUpdate(subject, "other other", "other other"))
	assert.Equal(t, &subject.Status.AvailableUpdates[1], clusterversion.FindAvailableUpdate(subject, desiredImg, desiredVer))
}

func Test_LatestAvailableUpdate(t *testing.T) {
	subject := configv1.ClusterVersion{}
	assert.Nil(t, clusterversion.LatestAvailableUpdate(subject))

	subject.Status.AvailableUpdates = []configv1.Release{
		{Version: "4.5.12"},
		{Version: "4.5.23"},
		{Version: "4.5.3"},
	}
	assert.Equal(t, "4.5.23", clusterversion.LatestAvailableUpdate(subject).Version)
}
