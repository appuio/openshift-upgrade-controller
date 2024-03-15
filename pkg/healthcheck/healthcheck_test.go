package healthcheck_test

import (
	"fmt"
	"testing"

	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/appuio/openshift-upgrade-controller/pkg/healthcheck"
	configv1 "github.com/openshift/api/config/v1"
)

func Test_IsOperatorDegraded(t *testing.T) {
	t.Run("returns true if an operator is degraded", func(t *testing.T) {
		subject := configv1.ClusterVersion{
			Status: configv1.ClusterVersionStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:   configv1.OperatorDegraded,
						Status: configv1.ConditionTrue,
					},
				},
			},
		}
		assert.True(t, IsOperatorDegraded(subject))
	})

	t.Run("returns false if no operator is degraded", func(t *testing.T) {
		subject := configv1.ClusterVersion{
			Status: configv1.ClusterVersionStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:   configv1.OperatorDegraded,
						Status: configv1.ConditionFalse,
					},
				},
			},
		}
		assert.False(t, IsOperatorDegraded(subject))
		subject.Status.Conditions[0].Status = configv1.ConditionUnknown
		assert.False(t, IsOperatorDegraded(subject))
		subject.Status.Conditions = nil
		assert.False(t, IsOperatorDegraded(subject))
	})
}

func Test_MachineConfigPoolsUpdating(t *testing.T) {
	subject := machineconfigurationv1.MachineConfigPoolList{
		Items: []machineconfigurationv1.MachineConfigPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "master",
				},
				Status: machineconfigurationv1.MachineConfigPoolStatus{
					MachineCount:        3,
					UpdatedMachineCount: 2,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Name: "worker",
				},
				Status: machineconfigurationv1.MachineConfigPoolStatus{
					MachineCount:        3,
					UpdatedMachineCount: 3,
				},
			}, {
				ObjectMeta: metav1.ObjectMeta{
					Name: "x-storage",
				},
				Spec: machineconfigurationv1.MachineConfigPoolSpec{
					Paused: true,
				},
				Status: machineconfigurationv1.MachineConfigPoolStatus{
					MachineCount:        3,
					UpdatedMachineCount: 0,
				},
			},
		},
	}

	require.Equal(t,
		[]UpdatingPool{
			{
				Name:    "master",
				Total:   3,
				Updated: 2,
			}, {
				Name:    "x-storage",
				Paused:  true,
				Total:   3,
				Updated: 0,
			},
		},
		MachineConfigPoolsUpdating(subject),
	)

	require.Equal(t, "[master (2/3) x-storage [paused] (0/3)]", fmt.Sprint(MachineConfigPoolsUpdating(subject)))
}
