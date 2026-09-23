package v1beta1_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	batchtyped "k8s.io/client-go/kubernetes/typed/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	upgradejobhookv1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	webhookv1beta1 "github.com/appuio/openshift-upgrade-controller/internal/webhook/v1beta1"
)

var (
	// lifetime: setup before [testing.M.Run], torn down after [testing.M.Run] returns.
	envtestClient kubernetes.Interface
)

func TestMain(m *testing.M) {
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: discoverEnvtestBinDir(),
	}
	k8sCfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting envtest: %v\n", err)
		os.Exit(1)
	}
	envtestClient = kubernetes.NewForConfigOrDie(k8sCfg)

	code := m.Run()
	if err := testEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "error while stopping envtest: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

type mutator func(*upgradejobhookv1beta1.UpgradeJobHook)

var createTestCases = []struct {
	name     string
	mutate   mutator // nil = the valid baseline
	matchErr []string
}{
	{
		name: "valid hook template",
	},
	{
		name: "restartPolicy Always is rejected",
		mutate: func(h *upgradejobhookv1beta1.UpgradeJobHook) {
			h.Spec.Template.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
		},
		matchErr: []string{"spec.template.spec.template.spec.restartPolicy", `valid values: "OnFailure", "Never"`},
	},
	{
		name: "duplicate container names",
		mutate: func(h *upgradejobhookv1beta1.UpgradeJobHook) {
			ps := &h.Spec.Template.Spec.Template.Spec
			ps.Containers = append(ps.Containers, ps.Containers[0])
		},
		matchErr: []string{"Duplicate value"},
	},
	{
		name: "invalid template label value",
		mutate: func(h *upgradejobhookv1beta1.UpgradeJobHook) {
			h.Spec.Template.Labels["appuio-managed-upgrade"] = "not a label value!"
		},
		matchErr: []string{"spec.template.metadata.labels", "Invalid value"},
	},
}

func Test_UpgradeJobHookCustomValidator_ValidateCreate(t *testing.T) {
	v := webhookv1beta1.NewUpgradeJobHookCustomValidator(envtestClient)

	for _, tc := range createTestCases {
		t.Run(tc.name, func(t *testing.T) {
			hook := validHook()
			if tc.mutate != nil {
				tc.mutate(hook)
			}
			_, err := v.ValidateCreate(t.Context(), hook)
			if len(tc.matchErr) == 0 {
				require.NoError(t, err)
				return
			}
			assertInvalid(t, err, tc.matchErr)
		})
	}
}

var updateTestCases = []struct {
	name     string
	oldMut   mutator // nil = valid baseline
	newMut   mutator
	matchErr []string
}{
	{
		name:   "non-template change is allowed (status/finalizer-like update)",
		newMut: func(h *upgradejobhookv1beta1.UpgradeJobHook) { h.Labels["team"] = "appuio" },
	},
	{
		name: "template change to an invalid restartPolicy is rejected",
		newMut: func(h *upgradejobhookv1beta1.UpgradeJobHook) {
			h.Spec.Template.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
		},
		matchErr: []string{"spec.template.spec.template.spec.restartPolicy", `valid values: "OnFailure", "Never"`},
	},
	{
		// Documents the deliberate DeepEqual-skip caveat: hooks that predate
		// the webhook stay unvalidated until their template actually changes.
		name: "unchanged invalid template passes (grandfathered-hook caveat)",
		oldMut: func(h *upgradejobhookv1beta1.UpgradeJobHook) {
			h.Spec.Template.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
		},
		newMut: func(h *upgradejobhookv1beta1.UpgradeJobHook) {
			h.Spec.Template.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
		},
	},
}

func Test_UpgradeJobHookCustomValidator_ValidateUpdate(t *testing.T) {
	v := webhookv1beta1.NewUpgradeJobHookCustomValidator(envtestClient)

	for _, tc := range updateTestCases {
		t.Run(tc.name, func(t *testing.T) {
			oldHook, newHook := validHook(), validHook()
			if tc.oldMut != nil {
				tc.oldMut(oldHook)
			}
			if tc.newMut != nil {
				tc.newMut(newHook)
			}
			_, err := v.ValidateUpdate(t.Context(), oldHook, newHook)
			if len(tc.matchErr) == 0 {
				require.NoError(t, err)
				return
			}
			assertInvalid(t, err, tc.matchErr)
		})
	}
}

// unreachableClient fails the test loudly if any dry-run is attempted.
type unreachableClient struct{ kubernetes.Interface }

func (unreachableClient) BatchV1() batchtyped.BatchV1Interface {
	panic("dry-run client must not be called when the template is unchanged")
}

func Test_UpgradeJobHookCustomValidator_ValidateUpdate_SkipsCheckWhenTemplateUnchanged(t *testing.T) {
	oldHook := validHook()
	updated := oldHook.DeepCopy()
	updated.Labels["team"] = "appuio" // any non-template change

	v := webhookv1beta1.NewUpgradeJobHookCustomValidator(unreachableClient{})
	_, err := v.ValidateUpdate(t.Context(), oldHook, updated)
	require.NoError(t, err)
}

func Test_UpgradeJobHookCustomValidator_ValidateDelete(t *testing.T) {
	v := &webhookv1beta1.UpgradeJobHookCustomValidator{}
	_, err := v.ValidateDelete(t.Context(), &upgradejobhookv1beta1.UpgradeJobHook{})
	require.NoError(t, err, "ValidateDelete should not return an error")
}

// assertInvalid pins the structured denial: a 422/Invalid StatusError whose
// Details.Causes carry the remapped CR-relative field paths. Your validator
// returns the StatusError itself, so the causes are asserted directly — this
// is the CI version of the "structured vs flattened" battery question.
func assertInvalid(t *testing.T, err error, matchErr []string) {
	t.Helper()

	var statusErr *apierrors.StatusError
	require.ErrorAs(t, err, &statusErr)
	st := statusErr.Status()
	require.EqualValues(t, 422, st.Code)
	require.Equal(t, metav1.StatusReasonInvalid, st.Reason)

	var haystack strings.Builder
	haystack.WriteString(st.Message)
	if st.Details != nil {
		for _, c := range st.Details.Causes {
			fmt.Fprintf(&haystack, "\n%s: %s", c.Field, c.Message)
		}
	}
	for _, match := range matchErr {
		assert.Contains(t, haystack.String(), match)
	}
}

// discoverEnvtestBinDir locates bin/k8s/<version> relative to the repo root,
// regardless of which package's test is running. Populated by `make test`
// (setup-envtest --bin-dir). Returns "" when absent — envtest then honors
// KUBEBUILDER_ASSETS, or fails loudly.
func discoverEnvtestBinDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for range 6 { // enough levels for any internal/... layout
		candidate := filepath.Join(dir, "bin", "k8s")
		if entries, err := os.ReadDir(candidate); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					return filepath.Join(candidate, e.Name())
				}
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// validHook is the original 'replace-nodes' example manifest as a fixture.
// Labels map is initialized — update mutators write to it (nil-map panic guard).
func validHook() *upgradejobhookv1beta1.UpgradeJobHook {
	return &upgradejobhookv1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "replace-nodes",
			Namespace: "default",
			Labels:    map[string]string{},
		},
		Spec: upgradejobhookv1beta1.UpgradeJobHookSpec{
			Template: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"appuio-managed-upgrade": "true"},
				},
				Spec: batchv1.JobSpec{
					ActiveDeadlineSeconds: new(int64(3600)),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy:      corev1.RestartPolicyNever,
							ServiceAccountName: "hook-manager",
							Containers: []corev1.Container{{
								Name:    "replace-nodes",
								Image:   "quay.io/appuio/oc:v4.20",
								Command: []string{"sh"},
								Args:    []string{"-c", "kubectl get nodes"},
							}},
						},
					},
				},
			},
		},
	}
}
