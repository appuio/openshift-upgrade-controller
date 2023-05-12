package healthcheck

import (
	configv1 "github.com/openshift/api/config/v1"
)

// IsOperatorDegraded returns whether an operator is degraded in the cluster
func IsOperatorDegraded(cv configv1.ClusterVersion) bool {
	for _, c := range cv.Status.Conditions {
		if c.Type == configv1.OperatorDegraded && c.Status == configv1.ConditionTrue {
			return true
		}
	}
	return false
}
