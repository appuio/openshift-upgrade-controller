package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpgradeSuspensionWindowSpec defines the desired state of UpgradeSuspensionWindow
type UpgradeSuspensionWindowSpec struct {
	// Start is the time when the suspension window starts.
	// +kubebuilder:validation:Required
	// +required
	Start metav1.Time `json:"start"`
	// End is the time when the suspension window ends.
	// +kubebuilder:validation:Required
	// +required
	End    metav1.Time `json:"end"`
	Reason string      `json:"reason"`

	// ConfigSelector is the selector for UpgradeConfigs to suspend upgrades for.
	// An empty label selector matches all objects. A null label selector matches no objects.
	// Matching UpgradeConfig objects won’t create UpgradeJob objects during the time window.
	ConfigSelector *metav1.LabelSelector `json:"configSelector,omitempty"`
	// JobSelector is the selector for UpgradeJobs to suspend upgrades for.
	// An empty label selector matches all objects. A null label selector matches no objects.
	// Matching UpgradeJob objects won’t start the upgrade during the time window.
	// Skipped jobs will be marked as successful with reason skipped.
	// Success and Finish hooks will be executed as normal.
	// If the job was owned by a UpgradeConfig object, the object creates a new job with the current (possibly same) version in the next non-suspended time window.
	// Already running jobs will be allowed to finish.
	JobSelector *metav1.LabelSelector `json:"jobSelector,omitempty"`
}

// UpgradeSuspensionWindowStatus defines the observed state of UpgradeSuspensionWindow
type UpgradeSuspensionWindowStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// UpgradeSuspensionWindow is the Schema for the upgradejobs API
type UpgradeSuspensionWindow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UpgradeSuspensionWindowSpec   `json:"spec,omitempty"`
	Status UpgradeSuspensionWindowStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// UpgradeSuspensionWindowList contains a list of UpgradeSuspensionWindow
type UpgradeSuspensionWindowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UpgradeSuspensionWindow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UpgradeSuspensionWindow{}, &UpgradeSuspensionWindowList{})
}
