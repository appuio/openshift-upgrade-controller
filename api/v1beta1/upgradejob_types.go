package v1beta1

import (
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// UpgradeJobConditionFailed is the condition type for a failed upgrade job
	UpgradeJobConditionFailed = "Failed"
	// UpgradeJobConditionSucceeded is the condition type for a succeeded upgrade job
	UpgradeJobConditionSucceeded = "Succeeded"
	// UpgradeJobConditionStarted is the condition type for a started upgrade job
	UpgradeJobConditionStarted = "Started"
	// UpgradeJobConditionPreHealthCheckDone is the condition type for a pre health check done upgrade job
	UpgradeJobConditionPreHealthCheckDone = "PreHealthCheckDone"
	// UpgradeJobConditionUpgradeCompleted is the condition type for a started upgrade job
	UpgradeJobConditionUpgradeCompleted = "UpgradeCompleted"
	// UpgradeJobConditionPostHealthCheckDone is the condition type for a post health check done upgrade job
	UpgradeJobConditionPostHealthCheckDone = "PostHealthCheckDone"

	// UpgradeJobReasonFailed is the generic reason for a failed upgrade job
	UpgradeJobReasonFailed = "Failed"
	// UpgradeJobReasonExpired is used when the upgrade job is not started before the startBefore time
	UpgradeJobReasonExpired = "Expired"
	// UpgradeJobReasonUpgradeWithdrawn is used when the upgrade was retracted by the upstream after the upgrade job was created
	UpgradeJobReasonUpgradeWithdrawn = "UpgradeBecameUnavailable"
)

// UpgradeJobSpec defines the desired state of UpgradeJob
type UpgradeJobSpec struct {
	// StartAfter defines the time after which the upgrade job should start
	StartAfter metav1.Time `json:"startAfter"`
	// StartBefore defines the time before which the upgrade job should start.
	// If the upgrade job is not started before this time, it is considered failed.
	StartBefore metav1.Time `json:"startBefore"`

	// DesiredVersion defines the desired version to upgrade to
	DesiredVersion configv1.Update `json:"desiredVersion"`

	// UpgradeJobConfig defines the configuration for the upgrade job
	UpgradeJobConfig `json:"config"`
}

// UpgradeJobConfig defines the configuration for the upgrade job
type UpgradeJobConfig struct {
	// UpgradeTimeout defines the timeout after which the upgrade is considered failed
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:default:="12h"
	UpgradeTimeout metav1.Duration `json:"upgradeTimeout"`

	// PreUpgradeHealthChecks defines the health checks to be performed before the upgrade
	PreUpgradeHealthChecks UpgradeJobHealthCheck `json:"preUpgradeHealthChecks"`
	// PostUpgradeHealthChecks defines the health checks to be performed after the upgrade
	PostUpgradeHealthChecks UpgradeJobHealthCheck `json:"postUpgradeHealthChecks"`
}

// UpgradeJobHealthCheck defines the health checks to be performed
type UpgradeJobHealthCheck struct {
	// Timeout defines the timeout after which the health check is considered failed
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:default:="30m"
	Timeout metav1.Duration `json:"timeout"`

	// SkipDegradedOperatorsCheck defines whether to check the ClusterVersion object for degraded operators when performing the health check
	// +optional
	SkipDegradedOperatorsCheck bool `json:"skipDegradedOperatorsCheck"`
}

// UpgradeJobStatus defines the observed state of UpgradeJob
type UpgradeJobStatus struct {
	// Conditions is a list of conditions for the UpgradeJob
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// UpgradeJob is the Schema for the upgradejobs API
type UpgradeJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UpgradeJobSpec   `json:"spec,omitempty"`
	Status UpgradeJobStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// UpgradeJobList contains a list of UpgradeJob
type UpgradeJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UpgradeJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UpgradeJob{}, &UpgradeJobList{})
}
