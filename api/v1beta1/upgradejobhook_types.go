package v1beta1

import (
	"golang.org/x/exp/slices"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpgradeEvent is the type for upgrade events.
// +kubebuilder:validation:Enum=Create;Start;UpgradeComplete;Finish;Success;Failure
type UpgradeEvent string

func (e UpgradeEvent) InfluencesOutcome() bool {
	return slices.Contains(eventsInfluencingOutcome, e)
}

const (
	// EventCreate is the event type for when a job is created.
	// The version is pinned at this point and the job is waiting for startAfter.
	// This can be used to communicate the pending upgrade to other systems.
	// See `pinVersionWindow` in `UpgradeConfig`.
	EventCreate UpgradeEvent = "Create"
	// EventStart is the event type for when a job is started.
	EventStart UpgradeEvent = "Start"
	// UpgradeCompleted is the event type for when the upgrade is completed and health checks have passed.
	EventUpgradeComplete UpgradeEvent = "UpgradeComplete"

	// EventFinish is the event type for when a job is finished regardless of outcome.
	EventFinish UpgradeEvent = "Finish"
	// EventSuccess is the event type for when a job is finished successfully.
	EventSuccess UpgradeEvent = "Success"
	// EventFailure is the event type for when a job is finished with a failure.
	EventFailure UpgradeEvent = "Failure"

	// FailurePolicyAbort is the failure policy for aborting the upgrade.
	FailurePolicyAbort = "Abort"
	// FailurePolicyIgnore is the failure policy for ignoring the failure.
	FailurePolicyIgnore = "Ignore"

	// RunNext is the run type for running the hook for the next job.
	RunNext = "Next"
	// RunAll is the run type for running the hook for all jobs.
	RunAll = "All"
)

// eventsInfluencingOutcome is the list of events that influence the outcome of the upgrade.
var eventsInfluencingOutcome = []UpgradeEvent{
	EventCreate,
	EventStart,
	EventUpgradeComplete,
}

// UpgradeJobHookSpec defines the desired state of UpgradeJobHook
type UpgradeJobHookSpec struct {
	// On is the list of events to trigger the hook to be executed.
	// `Create`, `Start`, and `UpgradeComplete` are the events that influence the outcome of the upgrade.
	// `Finish`, `Success`, and `Failure` do not influence the outcome of the upgrade,
	// Job completion will not be checked, they are only used for informational purposes.
	On []UpgradeEvent `json:"on,omitempty"`
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

func (s UpgradeJobHookSpec) GetOn() []UpgradeEvent {
	if s.On == nil {
		return []UpgradeEvent{}
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
