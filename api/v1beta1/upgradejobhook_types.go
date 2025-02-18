package v1beta1

import (
	"golang.org/x/exp/slices"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UpgradeEvent is the type for upgrade events.
// +kubebuilder:validation:Enum=Create;Start;UpgradeComplete;MachineConfigPoolUnpause;Finish;Success;Failure
type UpgradeEvent string

// eventsInfluencingOutcome is the list of events that influence the outcome of the upgrade.
var eventsInfluencingOutcome = []UpgradeEvent{
	EventCreate,
	EventStart,
	EventUpgradeComplete,
	EventMachineConfigPoolUnpause,
}

func (e UpgradeEvent) InfluencesOutcome() bool {
	return slices.Contains(eventsInfluencingOutcome, e)
}

const (
	// !!!!!! ADDING NEW EVENTS:
	// - Add the event to the `UpgradeEvent` type.
	// - Update the Enum validation on the `UpgradeEvent` type.
	// - Add the event to the `eventsInfluencingOutcome` list if required.
	// !!!!!! UPDATING EVENT DESCRIPTIONS:
	// - Update the event description in the `UpgradeEvent` type.
	// - Copy the description to the `UpgradeJobHookSpec.Events` field.

	// EventCreate is the event type for when a job is created.
	// The version is pinned at this point and the job is waiting for startAfter.
	// This can be used to communicate the pending upgrade to other systems.
	// See `pinVersionWindow` in `UpgradeConfig`.
	EventCreate UpgradeEvent = "Create"
	// EventStart is the event type for when a job is started.
	EventStart UpgradeEvent = "Start"
	// UpgradeCompleted is the event type for when the upgrade is completed and health checks have passed,
	// but before any paused MachineConfigPools are done upgrading.
	EventUpgradeComplete UpgradeEvent = "UpgradeComplete"

	// EventMachineConfigPoolUnpause is the event type for when a machine config pool is unpaused.
	// This event can only influence the outcome of the upgrade if the upgrade is not yet in a final state.
	// Consumers of this event might want to check the `EVENT_reason` environment variable to see if the pool was unpaused because of a delay or because the upgrade is in a final state.
	// We do not know if the pool needs updating in advance, thus the upgrade might complete without the pool being unpaused.
	// - Reason: `"DelayReached"` if the pool was unpaused regularly during a full upgrade.
	// - Reason: `"Completed"` if the pool was unpaused because the upgrade finished without changes to the pool.
	// The name of the unpaused pool is available in the `EVENT_pool` environment variable.
	EventMachineConfigPoolUnpause UpgradeEvent = "MachineConfigPoolUnpause"

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

// UpgradeJobHookSpec defines the desired state of UpgradeJobHook
type UpgradeJobHookSpec struct {
	// Events is the list of events to trigger the hook to be executed.
	// Events should be idempotent and not assume any prior events have been executed.
	// `Create`, `Start`, and `UpgradeComplete` are the events that influence the outcome of the upgrade.
	// `MachineConfigPoolUnpause` can influence the outcome of the upgrade if the upgrade is not yet in a final state.
	// `Finish`, `Success`, and `Failure` do not influence the outcome of the upgrade,
	// Job completion will not be checked, they are only used for informational purposes.
	// `Create` is the event type for when a job is created.
	// The version is pinned at this point and the job is waiting for startAfter.
	// This can be used to communicate the pending upgrade to other systems.
	// See `pinVersionWindow` in `UpgradeConfig`.
	// `Start` is the event type for when a job is started.
	// `UpgradeCompleted` is the event type for when the upgrade is completed and health checks have passed,
	// but before any paused MachineConfigPools are done upgrading.
	// `MachineConfigPoolUnpause` is the event type for when a machine config pool is unpaused.
	// This event can only influence the outcome of the upgrade if the upgrade is not yet in a final state.
	// Consumers of this event might want to check the `EVENT_reason` environment variable to see if the pool was unpaused because of a delay or because the upgrade is in a final state.
	// We do not know if the pool needs updating in advance, thus the upgrade might complete without the pool being unpaused.
	// - Reason: `"DelayReached"` if the pool was unpaused regularly during a full upgrade.
	// - Reason: `"Completed"` if the pool was unpaused because the upgrade finished without changes to the pool.
	// The name of the unpaused pool is available in the `EVENT_pool` environment variable.
	// `Finish` is the event type for when a job is finished regardless of outcome.
	// `Success` is the event type for when a job is finished successfully.
	// `Failure` is the event type for when a job is finished with a failure.
	Events []UpgradeEvent `json:"events,omitempty"`
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
	// Disruptive defines if the code run by the hook is potentially disruptive.
	// Added to the job metrics and injected as an environment variable to all hooks matching the job.
	// This is currently only informational, but can be used to make decisions in jobs.
	// The default is `false`.
	Disruptive bool `json:"disruptive,omitempty"`
	// Selector is the label selector that determines which upgrade jobs the hook is executed for.
	Selector metav1.LabelSelector `json:"selector,omitempty"`
	// Template is the job template that is executed.
	Template batchv1.JobTemplateSpec `json:"template,omitempty"`
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
	// ClaimedBy is the owner reference of the job that claimed the hook.
	// Only used for hooks with `run: Next`.
	ClaimedBy ClaimReference `json:"claimedBy,omitempty"`
}

// ClaimReference contains enough information to let you identify an owning
// object. An owning object must be in the same namespace as the dependent, or
// be cluster-scoped, so there is no namespace field.
// +structType=atomic
type ClaimReference struct {
	// API version of the referent.
	APIVersion string `json:"apiVersion" protobuf:"bytes,5,opt,name=apiVersion"`
	// Kind of the referent.
	Kind string `json:"kind" protobuf:"bytes,1,opt,name=kind"`
	// Name of the referent.
	Name string `json:"name" protobuf:"bytes,3,opt,name=name"`
	// UID of the referent.
	UID types.UID `json:"uid" protobuf:"bytes,4,opt,name=uid,casttype=k8s.io/apimachinery/pkg/types.UID"`
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

// Claim claims the hook for the given claimer.
// Returns true if the hook was claimed, or does not need to be claimed, false if it was already claimed.
// Second return value is true if the hooks status was updated.
func (u *UpgradeJobHook) Claim(claimer client.Object) (ok, updated bool) {
	if u.Spec.GetRun() != RunNext {
		return true, false
	}

	ref := buildClaimReference(claimer)
	if u.Status.ClaimedBy == ref {
		return true, false
	}
	if u.Status.ClaimedBy == (ClaimReference{}) {
		u.Status.ClaimedBy = ref
		return true, true
	}

	return false, false
}

// WouldExecute returns true if the hook would be executed for the given claimer.
func (u *UpgradeJobHook) WouldExecute(claimer client.Object) bool {
	if u.Spec.GetRun() != RunNext {
		return true
	}

	return u.Status.ClaimedBy == (ClaimReference{}) || u.Status.ClaimedBy == buildClaimReference(claimer)
}

func buildClaimReference(claimer client.Object) ClaimReference {
	return ClaimReference{
		APIVersion: claimer.GetObjectKind().GroupVersionKind().GroupVersion().String(),
		Kind:       claimer.GetObjectKind().GroupVersionKind().Kind,
		Name:       claimer.GetName(),
		UID:        claimer.GetUID(),
	}
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
