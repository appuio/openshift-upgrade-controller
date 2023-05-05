package clusterversion

import (
	"reflect"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	"golang.org/x/mod/semver"
)

// SpecEqualIgnoringDesiredUpdate returns whether the two ClusterVersionSpecs are equal, ignoring the desired update
func SpecEqualIgnoringDesiredUpdate(a, b configv1.ClusterVersionSpec) (equal bool) {
	a.DesiredUpdate = nil
	b.DesiredUpdate = nil
	return reflect.DeepEqual(a, b)
}

// IsUpgrading returns whether the cluster is upgrading
func IsUpgrading(cv configv1.ClusterVersion) bool {
	for _, condition := range cv.Status.Conditions {
		if condition.Type == configv1.OperatorProgressing && condition.Status == configv1.ConditionTrue {
			return true
		}
	}
	return false
}

// IsVersionUpgradeCompleted returns whether the version upgrade in desired version is completed
func IsVersionUpgradeCompleted(cv configv1.ClusterVersion) bool {
	for _, version := range cv.Status.History {
		if version.State == configv1.CompletedUpdate &&
			version.Image == cv.Spec.DesiredUpdate.Image &&
			version.Version == cv.Spec.DesiredUpdate.Version {

			return true
		}
	}

	return false
}

// FindAvailableUpdate returns the available update with the given image and version
func FindAvailableUpdate(cv configv1.ClusterVersion, image, version string) *configv1.Release {
	for _, update := range cv.Status.AvailableUpdates {
		if update.Version == version && update.Image == image {
			return &update
		}
	}
	return nil
}

// LatestAvailableUpdate returns the latest available update sorted by version
func LatestAvailableUpdate(cv configv1.ClusterVersion) *configv1.Release {
	updates := cv.Status.AvailableUpdates
	if len(updates) == 0 {
		return nil
	}

	sort.Slice(updates, func(i, j int) bool {
		return semver.Compare("v"+updates[i].Version, "v"+updates[j].Version) > 0
	})

	return &cv.Status.AvailableUpdates[0]
}
