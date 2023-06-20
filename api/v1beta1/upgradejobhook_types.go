package v1beta1

import (
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// EventCreate is the event type for when a job is created.
	// The version is pinned at this point and the job is waiting for startAfter.
	// This can be used to communicate the pending upgrade to other systems.
	// See `pinVersionWindow` in `UpgradeConfig`.
	EventCreate = "Create"
	// EventStart is the event type for when a job is started.
	EventStart = "Start"
	// EventFinish is the event type for when a job is finished regardless of outcome.
	EventFinish = "Finish"
	// EventSuccess is the event type for when a job is finished successfully.
	EventSuccess = "Success"
	// EventFailure is the event type for when a job is finished with a failure.
	EventFailure = "Failure"

	// FailurePolicyAbort is the failure policy for aborting the upgrade.
	FailurePolicyAbort = "Abort"
	// FailurePolicyIgnore is the failure policy for ignoring the failure.
	FailurePolicyIgnore = "Ignore"

	// RunNext is the run type for running the hook for the next job.
	RunNext = "Next"
	// RunAll is the run type for running the hook for all jobs.
	RunAll = "All"
)

// UpgradeJobHookSpec defines the desired state of UpgradeJobHook
type UpgradeJobHookSpec struct {
	// On is the list of events to trigger the hook to be executed.
	// +kubebuilder:validation:Enum=Create;Start;Finish;Success;Failure
	On []string `json:"on,omitempty"`
	// Run defines if the hook is executed for the `Next` or `All` jobs.
	// Defaults to `All`.
	// +kubebuilder:validation:Enum=Next;All
	Run string `json:"run,omitempty"`
	// FailurePolicy defines the policy for handling failures.
	// If `Abort` the upgrade is aborted and the job is marked as failed.
	// If `Ignore` the upgrade continues and the job is marked as success.
	// Defaults to `Ignore`.
	// More advanced failure policies can be handled through the upstream Job failure handling mechanisms.
	// +kubebuilder:validation:Enum=Abort;Ignore
	FailurePolicy string `json:"failurePolicy,omitempty"`
	// Selector is the label selector that determines which jobs the hook is executed for.
	Selector metav1.LabelSelector `json:"selector,omitempty"`
	// Template is the job template that is executed.
	Template batchv1.JobTemplateSpec `json:"template,omitempty"`
}

func (s UpgradeJobHookSpec) GetOn() []string {
	if s.On == nil {
		return []string{}
	}
	return s.On
}

func (s UpgradeJobHookSpec) GetRun() string {
	if s.Run == "" {
		return RunAll
	}
	return s.Run
}

func (s UpgradeJobHookSpec) GetFailurePolicy() string {
	if s.FailurePolicy == "" {
		return FailurePolicyIgnore
	}
	return s.FailurePolicy
}

// UpgradeJobHookStatus defines the observed state of UpgradeJobHook
type UpgradeJobHookStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// UpgradeJobHook is the Schema for the upgradejobhooks API
type UpgradeJobHook struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UpgradeJobHookSpec   `json:"spec,omitempty"`
	Status UpgradeJobHookStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// UpgradeJobHookList contains a list of UpgradeJobHook
type UpgradeJobHookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UpgradeJobHook `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UpgradeJobHook{}, &UpgradeJobHookList{})
}
