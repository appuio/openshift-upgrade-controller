package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeForceDrainSpec defines the desired state of NodeForceDrain
type NodeForceDrainSpec struct {
	// NodeSelector is a selector to select which nodes to drain
	// A nil selector matches no nodes, while an empty selector matches all nodes.
	NodeSelector *metav1.LabelSelector `json:"nodeSelector"`
	// NodeDrainGracePeriod is the duration until the controller starts to delete pods on the node.
	// The duration is calculated from the OpenShist node drain annotation.
	// This circumvents the eviction API and means that PDBs are ignored.
	// This is not a force delete, but it will delete pods that are not managed by a controller.
	// A zero value disables the force drain.
	NodeDrainGracePeriod metav1.Duration `json:"nodeDrainGracePeriod"`
	// PodForceDeleteGracePeriod is the duration until the controller starts to force delete pods on the node.
	// The duration is calculated from the pods deletion timestamp.
	// This is equivalent to the `--force --grace-period=0` flag of `kubectl delete pod`.
	// A zero value disables the force delete.
	PodForceDeleteGracePeriod metav1.Duration `json:"podForceDeleteGracePeriod"`
}

// NodeForceDrainStatus defines the observed state of NodeForceDrain
type NodeForceDrainStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// NodeForceDrain is the Schema for the NodeForceDrains API
type NodeForceDrain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeForceDrainSpec   `json:"spec,omitempty"`
	Status NodeForceDrainStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NodeForceDrainList contains a list of NodeForceDrain
type NodeForceDrainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeForceDrain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeForceDrain{}, &NodeForceDrainList{})
}
