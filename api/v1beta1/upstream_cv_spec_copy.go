package v1beta1

import (
	configv1 "github.com/openshift/api/config/v1"
)

// checks if the ConfigClusterVersionSpec is equal to configv1.ClusterVersionSpec.
// Will fail if upstream changes the type definition.
var _ = ConfigV1ClusterVersionSpec(configv1.ClusterVersionSpec{})

// ConfigV1ClusterVersionSpec is a copy of configv1.ClusterVersionSpec to allow for partial updates.
// It makes the cluster id optional.
// It is checked against configv1.ClusterVersionSpec to ensure that it is always up to date.
// TODO(bastjan): Make codegen generate this type.
type ConfigV1ClusterVersionSpec struct {
	// clusterID uniquely identifies this cluster. This is expected to be
	// an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in
	// hexadecimal values).
	//
	// +optional
	ClusterID configv1.ClusterID `json:"clusterID,omitempty"`

	// desiredUpdate is an optional field that indicates the desired value of
	// the cluster version. Setting this value will trigger an upgrade (if
	// the current version does not match the desired version). The set of
	// recommended update values is listed as part of available updates in
	// status, and setting values outside that range may cause the upgrade
	// to fail. You may specify the version field without setting image if
	// an update exists with that version in the availableUpdates or history.
	//
	// If an upgrade fails the operator will halt and report status
	// about the failing component. Setting the desired update value back to
	// the previous version will cause a rollback to be attempted. Not all
	// rollbacks will succeed.
	//
	// +optional
	DesiredUpdate *configv1.Update `json:"desiredUpdate,omitempty"`

	// upstream may be used to specify the preferred update server. By default
	// it will use the appropriate update server for the cluster and region.
	//
	// +optional
	Upstream configv1.URL `json:"upstream,omitempty"`
	// channel is an identifier for explicitly requesting that a non-default
	// set of updates be applied to this cluster. The default channel will be
	// contain stable updates that are appropriate for production clusters.
	//
	// +optional
	Channel string `json:"channel,omitempty"`

	// capabilities configures the installation of optional, core
	// cluster components.  A null value here is identical to an
	// empty object; see the child properties for default semantics.
	// +optional
	Capabilities *configv1.ClusterVersionCapabilitiesSpec `json:"capabilities,omitempty"`

	// signatureStores contains the upstream URIs to verify release signatures and optional
	// reference to a config map by name containing the PEM-encoded CA bundle.
	//
	// By default, CVO will use existing signature stores if this property is empty.
	// The CVO will check the release signatures in the local ConfigMaps first. It will search for a valid signature
	// in these stores in parallel only when local ConfigMaps did not include a valid signature.
	// Validation will fail if none of the signature stores reply with valid signature before timeout.
	// Setting signatureStores will replace the default signature stores with custom signature stores.
	// Default stores can be used with custom signature stores by adding them manually.
	//
	// A maximum of 32 signature stores may be configured.
	// +kubebuilder:validation:MaxItems=32
	// +openshift:enable:FeatureSets=CustomNoUpgrade;TechPreviewNoUpgrade
	// +listType=map
	// +listMapKey=url
	// +optional
	SignatureStores []configv1.SignatureStore `json:"signatureStores"`

	// overrides is list of overides for components that are managed by
	// cluster version operator. Marking a component unmanaged will prevent
	// the operator from creating or updating the object.
	// +optional
	Overrides []configv1.ComponentOverride `json:"overrides,omitempty"`
}
