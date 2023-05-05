package v1beta1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpgradeConfigSpec defines the desired state of UpgradeConfig
type UpgradeConfigSpec struct {
	// Schedule defines the schedule for the upgrade
	Schedule UpgradeConfigSchedule `json:"schedule"`
	// PinVersionWindow defines the time window before the maintenance window in which the upgrade version is pinned.
	// `UpgradeJobs` are created at `schedule - pinVersionWindow`.
	// +optional
	PinVersionWindow time.Duration `json:"pinVersionWindow"`
	// MaxSchedulingDelay defines the maximum time after which the upgrade job should be scheduled.
	// If the upgrade job is not scheduled before this time, it will not be scheduled.
	MaxSchedulingDelay time.Duration `json:"maxSchedulingDelay"`
	// MaxUpgradeStartDelay defines the maximum time after which the upgrade job should be started.
	// If the upgrade job is not started before this time, it is considered failed.
	// +optional
	MaxUpgradeStartDelay time.Duration `json:"maxUpgradeStartDelay"`

	// JobTemplate defines the template for the upgrade job
	JobTemplate UpgradeConfigJobTemplate `json:"jobTemplate"`
}

// UpgradeJobSpec defines the desired state of UpgradeJob
type UpgradeConfigJobTemplate struct {
	Spec UpgradeJobSpec `json:"spec"`
}

// UpgradeConfigSchedule defines the schedule for the upgrade
type UpgradeConfigSchedule struct {
	// Cron defines the cron schedule for the upgrade as per https://pkg.go.dev/github.com/robfig/cron/v3#hdr-CRON_Expression_Format
	Cron string `json:"cron"`
	// IsoWeek defines the week of the year according to ISO 8601 week number to schedule the upgrade.
	// Currently supported values are `@odd` and `@even`.
	// +optional
	IsoWeek string `json:"isoWeek"`
	// Location defines the location to use for the cron schedule. Defaults to the local time zone.
	// +optional
	Location string `json:"location"`
	// Suspend defines whether the upgrade should be suspended. Defaults to false.
	// +optional
	Suspend bool `json:"suspend"`
}

// UpgradeConfigStatus defines the observed state of UpgradeConfig
type UpgradeConfigStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// UpgradeConfig is the Schema for the upgradeconfigs API
type UpgradeConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UpgradeConfigSpec   `json:"spec,omitempty"`
	Status UpgradeConfigStatus `json:"status,omitempty"`
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
