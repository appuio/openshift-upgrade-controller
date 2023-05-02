package clusterversion

import (
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
)

func SpecEqualIgnoringDesiredUpdate(a, b configv1.ClusterVersionSpec) (equal bool) {
	a.DesiredUpdate = nil
	b.DesiredUpdate = nil
	return reflect.DeepEqual(a, b)
}
