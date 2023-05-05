package healthcheck_test

import (
	"testing"

	. "github.com/appuio/openshift-upgrade-controller/pkg/healthcheck"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
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
