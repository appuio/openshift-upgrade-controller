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
	// UpgradeJobConditionPaused is the condition type for a paused upgrade job.
	// A upgrade job can be paused if `.spec.machineConfigPools` matches a pool and `delayUpgrade` is set.
	UpgradeJobConditionPaused = "Paused"
	// UpgradeJobConditionMachineConfigPoolsPaused is true if the controller paused any machine config pools.
	// Does not correlate with any upgrade specific condition.
	UpgradeJobConditionMachineConfigPoolsPaused = "MachineConfigPoolsPaused"

	// UpgradeJobReasonFailed is the generic reason for a failed upgrade job
	UpgradeJobReasonFailed = "Failed"
	// UpgradeJobReasonExpired is used when the upgrade job is not started before the startBefore time
	UpgradeJobReasonExpired = "Expired"
	// UpgradeJobReasonUnpausingPoolsExpired is used when the upgrade job was not able to unpause the machine config pools before the delayMax time
	UpgradeJobReasonUnpausingPoolsExpired = "UnpausingPoolsExpired"
	// UpgradeJobReasonTimedOut is used when the upgrade job is not completed before the upgradeTimeout time
	UpgradeJobReasonTimedOut = "TimedOut"
	// UpgradeJobReasonPreHealthCheckFailed is used when the health check failed
	UpgradeJobReasonPreHealthCheckFailed = "PreHealthCheckFailed"
	// UpgradeJobReasonPostHealthCheckFailed is used when the health check failed
	UpgradeJobReasonPostHealthCheckFailed = "PostHealthCheckFailed"
	// UpgradeJobReasonUpgradeWithdrawn is used when the upgrade was retracted by the upstream after the upgrade job was created
	UpgradeJobReasonUpgradeWithdrawn = "UpgradeWithdrawn"
	// UpgradeJobReasonHookFailed is used when a hook failed
	UpgradeJobReasonHookFailed = "HookFailed"
	// UpgradeJobReasonStarted is used when a step of the upgrade job was started
	UpgradeJobReasonStarted = "Started"
	// UpgradeJobReasonSucceeded is used when a step of the upgrade job did succeed
	UpgradeJobReasonSucceeded = "Succeeded"
	// UpgradeJobReasonCompleted is used when a step of the upgrade job did succeed
	UpgradeJobReasonCompleted = "Completed"
	// UpgradeJobReasonInProgress is used when the pre health check was done
	UpgradeJobReasonInProgress = "InProgress"
	// UpgradeJobReasonNoManagedPools is used when no machine config pools are managed by the upgrade job
	UpgradeJobReasonNoManagedPools = "NoManagedPools"
	// UpgradeJobReasonDelaySet is used if the upgrade job paused machine config pools due to delayUpgrade
	UpgradeJobReasonDelaySet = "DelaySet"
)

// UpgradeJobSpec defines the desired state of UpgradeJob
type UpgradeJobSpec struct {
	// StartAfter defines the time after which the upgrade job should start
	StartAfter metav1.Time `json:"startAfter"`
	// StartBefore defines the time before which the upgrade job should start.
	// If the upgrade job is not started before this time, it is considered failed.
	StartBefore metav1.Time `json:"startBefore"`

	// DesiredVersion defines the desired version to upgrade to.
	// Can be empty if the upgrade job was created when there was no new version available.
	// +optional
	DesiredVersion *configv1.Update `json:"desiredVersion,omitempty"`

	// UpgradeJobConfig defines the configuration for the upgrade job
	UpgradeJobConfig `json:"config"`
}

// UpgradeJobConfig defines the configuration for the upgrade job
type UpgradeJobConfig struct {
	// UpgradeTimeout defines the timeout after which the upgrade is considered failed.
	// Relative to the `.spec.startAfter` timestamp of the upgrade job.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:default:="12h"
	UpgradeTimeout metav1.Duration `json:"upgradeTimeout"`

	// PreUpgradeHealthChecks defines the health checks to be performed before the upgrade
	PreUpgradeHealthChecks UpgradeJobHealthCheck `json:"preUpgradeHealthChecks"`
	// PostUpgradeHealthChecks defines the health checks to be performed after the upgrade
	PostUpgradeHealthChecks UpgradeJobHealthCheck `json:"postUpgradeHealthChecks"`

	// MachineConfigPools defines the machine config pool specific configuration for the upgrade job
	// +optional
	MachineConfigPools []UpgradeJobMachineConfigPoolSpec `json:"machineConfigPools,omitempty"`
}

// UpgradeJobMachineConfigPoolSpec allows configuring the upgrade of a machine config pool
type UpgradeJobMachineConfigPoolSpec struct {
	// MatchLabels defines the labels to match the machine config pool.
	// If empty, all machine config pools are matched.
	// If nil, no machine config pools are matched.
	// +optional
	MatchLabels *metav1.LabelSelector `json:"matchLabels,omitempty"`

	// DelayUpgrade defines whether to delay the upgrade of the machine config pool
	// +optional
	DelayUpgrade UpgradeJobMachineConfigPoolDelayUpgradeSpec `json:"delayUpgrade,omitempty"`
}

// UpgradeJobMachineConfigPoolDelayUpgradeSpec defines the delay for the upgrade of a machine config pool
type UpgradeJobMachineConfigPoolDelayUpgradeSpec struct {
	// DelayMin defines the delay after which the upgrade of the machine config pool should start.
	// Relative to the `.spec.startAfter` timestamp of the upgrade job.
	// +optional
	DelayMin metav1.Duration `json:"delayMin,omitempty"`
	// DelayMax defines the maximum delay after which the upgrade of the machine config pool should start.
	// Relative to the `.spec.startBefore` timestamp of the upgrade job.
	// If the upgrade of the machine config pool can't be started before this time, it is considered failed.
	// +optional
	DelayMax metav1.Duration `json:"delayMax,omitempty"`
}

// UpgradeJobHealthCheck defines the health checks to be performed
type UpgradeJobHealthCheck struct {
	// Timeout defines the timeout after which the health check is considered failed
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:default:="1h"
	Timeout metav1.Duration `json:"timeout"`

	// SkipDegradedOperatorsCheck defines whether to check the ClusterVersion object for degraded operators when performing the health check
	// +optional
	SkipDegradedOperatorsCheck bool `json:"skipDegradedOperatorsCheck"`
}

// UpgradeJobStatus defines the observed state of UpgradeJob
type UpgradeJobStatus struct {
	// Conditions is a list of conditions for the UpgradeJob
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// HookJobTracker keeps track of the hooks that have been executed
	HookJobTracker []HookJobTracker `json:"hookTracker,omitempty"`
}

// HookJobTracker keeps track of the hooks that have been executed
type HookJobTracker struct {
	// HookEvent is the event of the hook
	HookEvent string `json:"hookEvent,omitempty"`
	// UpdgradeJobHook is the hook that was executed
	UpgradeJobHookName string `json:"upgradeJobHook,omitempty"`

	// Status is the status of the hook
	Status HookJobTrackerStatus `json:"status,omitempty"`
	// Message is the message for the status
	Message string `json:"message,omitempty"`
}

type HookJobTrackerStatus string

const (
	HookJobTrackerStatusComplete HookJobTrackerStatus = "complete"
	HookJobTrackerStatusFailed   HookJobTrackerStatus = "failed"
	HookJobTrackerStatusActive   HookJobTrackerStatus = "active"
)

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
