/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// NodeForceDrainReconciler reconciles the NodeForceDrain object
// It should be called on node change events.
// It force drains nodes after a certain period of time.
type NodeForceDrainReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile reacts to Node changes and force drains nodes after a certain period of time.
func (r *NodeForceDrainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeForceDrainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	nm := handler.TypedEnqueueRequestsFromMapFunc(NodeToNodeForceDrainMapper(mgr.GetClient()))
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedupgradev1beta1.NodeForceDrain{}).
		WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.Node{}, nm)).
		Complete(r)
}

// NodeToNodeForceDrainMapper maps Nodes to NodeForceDrain requests.
// It matches the nodes labels to the NodeForceDrain.spec.nodeSelector.
func NodeToNodeForceDrainMapper(c client.Reader) handler.TypedMapFunc[*corev1.Node, reconcile.Request] {
	return func(ctx context.Context, node *corev1.Node) []reconcile.Request {
		l := log.FromContext(ctx).WithName("NodeToNodeForceDrainMapper")

		var fds managedupgradev1beta1.NodeForceDrainList
		if err := c.List(ctx, &fds); err != nil {
			l.Error(err, "failed to list NodeForceDrain manifests")
			return []reconcile.Request{}
		}

		requests := make([]reconcile.Request, 0, len(fds.Items))
		for _, fd := range fds.Items {
			l := l.WithValues("force_node_drain_name", fd.Name, "force_node_drain_namespace", fd.Namespace)

			if fd.Spec.NodeSelector == nil {
				continue
			}
			sel, err := metav1.LabelSelectorAsSelector(fd.Spec.NodeSelector)
			if err != nil {
				l.Error(err, "failed to parse NodeSelector")
				continue
			}
			if !sel.Matches(labels.Set(node.GetLabels())) {
				continue
			}
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&fd)})
		}

		return requests
	}
}
