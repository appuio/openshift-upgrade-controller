package controllers

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_NodeReconciler_Reconcile(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(node("node1", "a", "a")).
		Build()

	subject := &NodeReconciler{
		Client: client,
		Scheme: scheme,
	}

	_, err := subject.Reconcile(ctx, requestFor("node1"))
	require.NoError(t, err)
	compareMetrics(t, "node1", 0.0, "node is not draining if desired drainer is the same as last applied drainer annotation")

	require.NoError(t, client.Update(ctx, node("node1", "b", "a")))
	_, err = subject.Reconcile(ctx, requestFor("node1"))
	require.NoError(t, err)
	compareMetrics(t, "node1", 1.0, "node should be draining if desired drainer is different from last applied drainer annotation")
}

func compareMetrics(t *testing.T, nodeLbl string, expected float64, msgAndArgs ...interface{}) {
	t.Helper()

	m, err := nodeDraining.GetMetricWithLabelValues(nodeLbl)
	require.NoError(t, err)
	require.Equal(t, expected, testutil.ToFloat64(m), msgAndArgs...)
}

func requestFor(name string) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}
}

func node(name, desiredDrainer, lastAppliedDrainer string) *corev1.Node {
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
