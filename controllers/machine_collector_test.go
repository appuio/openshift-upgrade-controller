package controllers

import (
	"strings"
	"testing"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_MachineCollector(t *testing.T) {
	const machineNamespace = "openshift-machine-api"

	scheme := runtime.NewScheme()
	require.NoError(t, machinev1beta1.AddToScheme(scheme))

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(
			&machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-empty",
					Namespace: machineNamespace,
				},
			},
			&machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine",
					Namespace: machineNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind:       "MachineSet",
							Name:       "machineset",
							Controller: ptr.To(true),
						},
					},
				},
				Spec: machinev1beta1.MachineSpec{
					ProviderID: ptr.To("provider-id"),
				},
				Status: machinev1beta1.MachineStatus{
					NodeRef: &corev1.ObjectReference{
						Name: "node-ref",
					},
					Phase: ptr.To("Running"),
				},
			},
			&machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-in-other-namespace",
					Namespace: machineNamespace + "-other",
				},
			},
		).
		Build()

	subject := &MachineCollector{
		Client:           client,
		MachineNamespace: machineNamespace,
	}

	metrics := `
	# HELP openshift_upgrade_controller_machine_info OpenShift Machine information
	# TYPE openshift_upgrade_controller_machine_info gauge
	openshift_upgrade_controller_machine_info{machine="machine",machineset="machineset",node_ref="node-ref",phase="Running",provider_id="provider-id"} 1
	openshift_upgrade_controller_machine_info{machine="machine-empty",machineset="",node_ref="",phase="",provider_id=""} 1
	`

	require.NoError(t,
		testutil.CollectAndCompare(subject, strings.NewReader(metrics), "openshift_upgrade_controller_machine_info"),
	)
}
