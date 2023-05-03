package clusterversion

import (
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
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
