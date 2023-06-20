package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpgradeConfigSpec defines the desired state of UpgradeConfig
type UpgradeConfigSpec struct {
	// Schedule defines the schedule for the upgrade
	Schedule UpgradeConfigSchedule `json:"schedule"`
	// PinVersionWindow defines the time window before the maintenance window in which the upgrade version is pinned.
	// `UpgradeJobs` are created at `schedule - pinVersionWindow`.
	// +optional
	PinVersionWindow metav1.Duration `json:"pinVersionWindow"`
	// MaxSchedulingDelay defines the maximum time after which the upgrade job should be scheduled.
	// If the upgrade job is not scheduled before this time, it will not be scheduled.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:default:="1h"
	MaxSchedulingDelay metav1.Duration `json:"maxSchedulingDelay"`
	// MaxUpgradeStartDelay defines the maximum time after which the upgrade job should be started.
	// If the upgrade job is not started before this time, it is considered failed.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:default:="1h"
	MaxUpgradeStartDelay metav1.Duration `json:"maxUpgradeStartDelay"`

	// JobTemplate defines the template for the upgrade job
	JobTemplate UpgradeConfigJobTemplate `json:"jobTemplate"`
}

// UpgradeConfigJobTemplate defines the desired state of UpgradeJob
type UpgradeConfigJobTemplate struct {
	// Standard object's metadata of the jobs created from this template.
	Metadata metav1.ObjectMeta `json:"metadata"`
	// Specification of the desired behavior of the job.
	Spec UpgradeConfigJobTemplateSpec `json:"spec"`
}

// UpgradeConfigJobTemplateSpec defines the desired state of UpgradeJob
type UpgradeConfigJobTemplateSpec struct {
	Config UpgradeJobConfig `json:"config"`
}

// UpgradeConfigSchedule defines the schedule for the upgrade
type UpgradeConfigSchedule struct {
	// Cron defines the cron schedule for the upgrade as per https://pkg.go.dev/github.com/robfig/cron/v3#hdr-CRON_Expression_Format
	Cron string `json:"cron"`
	// IsoWeek defines the week of the year according to ISO 8601 week number to schedule the upgrade.
	// Currently supported values are `@odd` and `@even`.
	// +optional
	// +kubebuilder:validation:Pattern:=`^(@odd|@even|\d{1,2})$`
	IsoWeek string `json:"isoWeek"`
	// Location defines the location to use for the cron schedule. Defaults to the local time zone.
	// +kubebuilder:default:=Local
	Location string `json:"location"`
	// Suspend defines whether the upgrade should be suspended. Defaults to false.
	// +optional
	Suspend bool `json:"suspend"`
}

// UpgradeConfigStatus defines the observed state of UpgradeConfig
type UpgradeConfigStatus struct {
	// LastScheduledUpgrade is the time at which the cluster version was last checked for updates.
	// Matches the startAfter time of the upgrade job that was created, or would have been created if an update was available.
	// Also is increased when a job would have been created, but was not created due to the config being suspended.
	// +optional
	LastScheduledUpgrade *metav1.Time `json:"lastScheduledUpgrade,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// UpgradeConfig is the Schema for the upgradeconfigs API
type UpgradeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec UpgradeConfigSpec `json:"spec"`
	// +optional
	Status UpgradeConfigStatus `json:"status"`
}

//+kubebuilder:object:root=true

// UpgradeConfigList contains a list of UpgradeConfig
type UpgradeConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UpgradeConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UpgradeConfig{}, &UpgradeConfigList{})
}
