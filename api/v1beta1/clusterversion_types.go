package v1beta1

import (
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterVersionSpec defines the desired state of ClusterVersion
type ClusterVersionSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Template is the template applied to the ClusterVersion object
	Template ClusterVersionTemplate `json:"template,omitempty"`

	Overlays []ClusterVersionOverlayConfig `json:"overlays,omitempty"`
}

// ClusterVersionStatus defines the observed state of ClusterVersion
type ClusterVersionStatus struct{}

type ClusterVersionTemplate struct {
	// Spec is the spec applied to the ClusterVersion object
	Spec configv1.ClusterVersionSpec `json:"spec,omitempty"`
}

type ClusterVersionOverlayConfig struct {
	// From is the time from which the overlay is applied.
	// +kubebuilder:validation:Required
	// +required
	From metav1.Time `json:"from"`
	// Overlay is the overlay applied to the base ClusterVersion object
	Overlay ClusterVersionOverlay `json:"overlay"`
}

type ClusterVersionOverlay struct {
	// Spec is the spec overlaid to the base ClusterVersion object
	Spec ConfigV1ClusterVersionSpec `json:"spec,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ClusterVersion is the Schema for the clusterversions API
type ClusterVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterVersionSpec   `json:"spec,omitempty"`
	Status ClusterVersionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterVersionList contains a list of ClusterVersion
type ClusterVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterVersion `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterVersion{}, &ClusterVersionList{})
}
