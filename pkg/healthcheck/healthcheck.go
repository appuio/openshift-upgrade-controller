package healthcheck

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
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

// UpdatingPool represents a machine config pool that is currently updating
type UpdatingPool struct {
	Name           string
	Total, Updated int
}

// String returns a string representation of the updating pool
func (p UpdatingPool) String() string {
	return fmt.Sprintf("%s (%d/%d)", p.Name, p.Updated, p.Total)
}

// MachineConfigPoolsUpdating returns a list of machine config pools that are currently updating
func MachineConfigPoolsUpdating(mcpl machineconfigurationv1.MachineConfigPoolList) []UpdatingPool {
	pools := make([]UpdatingPool, 0, len(mcpl.Items))
	for _, mcp := range mcpl.Items {
		if mcp.Status.MachineCount == mcp.Status.UpdatedMachineCount {
			continue
		}
		pools = append(pools, UpdatingPool{
			Name:    mcp.Name,
			Total:   int(mcp.Status.MachineCount),
			Updated: int(mcp.Status.UpdatedMachineCount),
		})
	}
	return pools
}
