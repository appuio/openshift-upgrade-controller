package controllers

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_NodeCollector(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(
			nodeWithDrainAnnotation("new-node1", "", ""),
			&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "new-node2",
				},
			},
			nodeWithDrainAnnotation("node", "a", "a"),
			nodeWithDrainAnnotation("node-draining1", "a", "b"),
			nodeWithDrainAnnotation("node-draining2", "a", ""),
		).
		Build()

	subject := &NodeCollector{
		Client: client,
	}

	metrics := `
	# HELP openshift_upgrade_controller_node_draining Node draining status
	# TYPE openshift_upgrade_controller_node_draining gauge
	openshift_upgrade_controller_node_draining{node="new-node1"} 0
	openshift_upgrade_controller_node_draining{node="new-node2"} 0
	openshift_upgrade_controller_node_draining{node="node"} 0
	openshift_upgrade_controller_node_draining{node="node-draining1"} 1
	openshift_upgrade_controller_node_draining{node="node-draining2"} 1
	`

	require.NoError(t,
		testutil.CollectAndCompare(subject, strings.NewReader(metrics), "openshift_upgrade_controller_node_draining"),
	)
}

func nodeWithDrainAnnotation(name, desiredDrainer, lastAppliedDrainer string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				DesiredDrainerAnnotationKey:     desiredDrainer,
				LastAppliedDrainerAnnotationKey: lastAppliedDrainer,
			},
		},
	}
}
