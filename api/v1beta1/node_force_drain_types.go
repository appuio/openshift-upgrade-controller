package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeForceDrainSpec defines the desired state of NodeForceDrain
type NodeForceDrainSpec struct {
	// NodeSelector is a selector to select which nodes to drain
	// An empty selector matches all nodes.
	NodeSelector metav1.LabelSelector `json:"nodeSelector"`
	// NamespaceSelector is a selector to select which namespaces to drain
	// An empty selector matches all namespaces.
	NamespaceSelector metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	// NodeDrainGracePeriod is the duration until the controller starts to delete pods on the node.
	// The duration is calculated from the OpenShist node drain annotation.
	// This circumvents the eviction API and means that PDBs are ignored.
	// This is not a force delete, but it will delete pods that are not managed by a controller.
	// A zero value disables the force drain.
	NodeDrainGracePeriod metav1.Duration `json:"nodeDrainGracePeriod"`
	// PodForceDeleteGracePeriod is the duration until the controller starts to force delete pods on the node.
	// The duration is calculated from the pods deletion timestamp.
	// Only pods on nodes that have reached the nodeDrainGracePeriod are force deleted.
	// The maximum time until pod force deletion is nodeDrainGracePeriod+podForceDeleteGracePeriod.
	// The deletion done is equivalent to the `--now` or the `--grace-period=1` flag of `kubectl delete pod`.
	// We do not use `--force --grace-period=0`, if doing so the pod would be dropped from the API without being killed on the node.
	// Such pods do block the reboot of the node and the node will stay in a `NotReady` state for extended periods.
	// A zero value disables the force delete.
	PodForceDeleteGracePeriod metav1.Duration `json:"podForceDeleteGracePeriod"`
}

// NodeForceDrainStatus defines the observed state of NodeForceDrain
type NodeForceDrainStatus struct {
	// LastObservedNodeDrain is the last time the controller observed a node drain.
	LastObservedNodeDrain []ObservedNodeDrain `json:"lastObservedNodeDrain,omitempty"`
}

type ObservedNodeDrain struct {
	// NodeName is the name of the node that was drained.
	NodeName string `json:"nodeName"`
	// LastAppliedDrain is a unique identifier for the drain.
	// Taken from the machineconfiguration.openshift.io/lastAppliedDrain annotation.
	LastAppliedDrain string      `json:"drainID"`
	ObservedTime     metav1.Time `json:"observedTime"`
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
