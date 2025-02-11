package controllers

import (
	"context"
	"testing"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func Test_NodeToNodeForceDrainMapper(t *testing.T) {
	fds := []client.Object{
		&managedupgradev1beta1.NodeForceDrain{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "fd-direct-match",
			},
			Spec: managedupgradev1beta1.NodeForceDrainSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"im-important": "true",
					},
				},
			},
		},
		&managedupgradev1beta1.NodeForceDrain{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "fd-direct-match-exp",
			},
			Spec: managedupgradev1beta1.NodeForceDrainSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "im-important",
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				},
			},
		},
		&managedupgradev1beta1.NodeForceDrain{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fd-selector-nil",
				Namespace: "default",
			},
			Spec: managedupgradev1beta1.NodeForceDrainSpec{
				NodeSelector: nil,
			},
		},
		&managedupgradev1beta1.NodeForceDrain{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fd-selector-no-match",
				Namespace: "default",
			},
			Spec: managedupgradev1beta1.NodeForceDrainSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"other-selector": "true",
					},
				},
			},
		},
	}

	c := controllerClient(t, fds...)
	subject := NodeToNodeForceDrainMapper(c)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-beef",
			Labels: map[string]string{
				"im-important": "true",
			},
		},
	}

	require.ElementsMatch(t, subject(context.Background(), node), []reconcile.Request{
		{NamespacedName: client.ObjectKeyFromObject(fds[0])},
		{NamespacedName: client.ObjectKeyFromObject(fds[1])},
	}, "should only return NodeForceDrain objects matching the input node")
}
