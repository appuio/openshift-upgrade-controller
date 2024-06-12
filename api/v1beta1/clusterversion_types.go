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
	// Overlays are the overlays applied to the base ClusterVersion object at the time specified in the From field.
	// Overlays do not combine, the overlay with the latest From timestamp is applied.
	// If two overlays have the same From timestamp, the behavior is undefined.
	// Overlays can't remove fields from the base ClusterVersion object.
	// You can work around this by setting the field to an empty value in the base ClusterVersion and applying an overlay in the past with the field set;
	// this allows the next overlay to remove the field.
	Overlays []ClusterVersionOverlayConfig `json:"overlays,omitempty"`
}

// ClusterVersionStatus defines the observed state of ClusterVersion
type ClusterVersionStatus struct {
	// Current is the ClusterVersion object that is currently applied.
	Current configv1.ClusterVersionSpec `json:"current,omitempty"`
	// Overlays shows the ClusterVersion objects that will be applied if their From timestamp is reached.
	Overlays []ClusterVersionStatusOverlays `json:"overlays,omitempty"`
	// OverlayApplied is the from timestamp of the currently applied overlay
	OverlayApplied metav1.Time `json:"overlayApplied,omitempty"`
}

// ClusterVersionStatusOverlays defines the ClusterVersion objects that will be applied if their From timestamp is reached.
type ClusterVersionStatusOverlays struct {
	// From is the time from which this preview will be applied.
	From metav1.Time `json:"from"`
	// Preview is the spec applied to the ClusterVersion object at the time of From.
	Preview ClusterVersionStatusPreview `json:"preview,omitempty"`
}

// ClusterVersionStatusPreview defines the spec applied to the ClusterVersion object.
type ClusterVersionStatusPreview struct {
	// Spec is the spec applied to the ClusterVersion object
	Spec configv1.ClusterVersionSpec `json:"spec,omitempty"`
}

// ClusterVersionTemplate defines the template applied to the ClusterVersion object.
type ClusterVersionTemplate struct {
	// Spec is the spec applied to the ClusterVersion object
	Spec configv1.ClusterVersionSpec `json:"spec,omitempty"`
}

// ClusterVersionOverlayConfig defines the overlay applied to the base ClusterVersion object.
type ClusterVersionOverlayConfig struct {
	// From is the time from which the overlay is applied.
	// +kubebuilder:validation:Required
	// +required
	From metav1.Time `json:"from"`
	// Overlay is the overlay applied to the base ClusterVersion object
	Overlay ClusterVersionOverlay `json:"overlay"`
}

// ClusterVersionOverlay defines the overlay applied to the base ClusterVersion object.
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
