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
	"fmt"
	"slices"
	"time"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"go.uber.org/multierr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
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
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups="apps",resources=daemonsets,verbs=get;list;watch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=nodeforcedrains,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=nodeforcedrains/status,verbs=get;update;patch

// Reconcile reacts to Node changes and force drains nodes after a certain period of time.
func (r *NodeForceDrainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.Reconcile")

	var fd managedupgradev1beta1.NodeForceDrain
	if err := r.Get(ctx, req.NamespacedName, &fd); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get NodeForceDrain: %w", err)
	}
	if fd.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	if fd.Spec.NodeSelector == nil {
		return ctrl.Result{}, nil
	}

	var nodes corev1.NodeList
	nodeSel, err := metav1.LabelSelectorAsSelector(fd.Spec.NodeSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse NodeSelector: %w", err)
	}
	if err := r.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: nodeSel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	drainingNodes := make([]corev1.Node, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		l := l.WithValues("node", node.Name)
		desiredDrain, ddOk := node.Annotations[DesiredDrainerAnnotationKey]
		lastAppliedDrain, laOk := node.Annotations[LastAppliedDrainerAnnotationKey]
		if !ddOk || !laOk {
			l.Info("Node is missing drain annotations. Not OCP?", "desiredDrain", desiredDrain, "lastAppliedDrain", lastAppliedDrain)
			continue
		}
		if desiredDrain == lastAppliedDrain {
			continue
		}
		drainingNodes = append(drainingNodes, node)
	}
	l.Info("Draining nodes", "nodes", drainingNodes)

	statusChanged := false
	for _, node := range drainingNodes {
		statusChanged = statusChanged || setLastObservedNodeDrain(&fd, node.Name, node.Annotations[LastAppliedDrainerAnnotationKey])
	}
	if statusChanged {
		l.Info("Observed new node drain timestamps", "lastObservedNodeDrains", fd.Status.LastObservedNodeDrain)
		if err := r.Status().Update(ctx, &fd); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update LastObservedNodeDrain status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	nodesOutOfGracePeriod := make([]corev1.Node, 0, len(drainingNodes))
	for _, node := range drainingNodes {
		ld, found := findLastObservedNodeDrain(fd.Status, node.Name)
		if !found {
			l.Info("Node was not found in status. Skipping", "node", node.Name)
			continue
		}
		if time.Now().Before(ld.ObservedTime.Add(fd.Spec.NodeDrainGracePeriod.Duration)) {
			l.Info("Node is still in grace period", "node", node.Name, "lastObservedNodeDrain", ld)
			continue
		}
		l.Info("Node is out of grace period", "node", node.Name, "lastObservedNodeDrain", ld, "nodeDrainGracePeriod", fd.Spec.NodeDrainGracePeriod)
		nodesOutOfGracePeriod = append(nodesOutOfGracePeriod, node)
	}

	if fd.Spec.NodeDrainGracePeriod.Duration != 0 {
		errors := make([]error, 0, len(nodesOutOfGracePeriod))
		for _, node := range nodesOutOfGracePeriod {
			l.Info("Force draining node", "node", node.Name)
			if err := r.forceDrainNode(ctx, node); err != nil {
				errors = append(errors, fmt.Errorf("failed to force drain node %s: %w", node.Name, err))
			}
		}
		if err := multierr.Combine(errors...); err != nil {
			return ctrl.Result{}, err
		}
	}

	if pfd := fd.Spec.PodForceDeleteGracePeriod.Duration; pfd != 0 {
		errors := make([]error, 0, len(nodesOutOfGracePeriod))
		for _, node := range nodesOutOfGracePeriod {
			l.Info("Force deleting pods on node", "node", node.Name)
			if err := r.forceDeletePodsOnNode(ctx, node, pfd); err != nil {
				errors = append(errors, fmt.Errorf("failed to force delete pods on node %s: %w", node.Name, err))
			}
		}
		if err := multierr.Combine(errors...); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// findLastObservedNodeDrain finds the last observed node drain in the status.
// Returns true if the node was found.
func findLastObservedNodeDrain(fd managedupgradev1beta1.NodeForceDrainStatus, nodeName string) (managedupgradev1beta1.ObservedNodeDrain, bool) {
	for _, o := range fd.LastObservedNodeDrain {
		if o.NodeName == nodeName {
			return o, true
		}
	}
	return managedupgradev1beta1.ObservedNodeDrain{}, false
}

// setLastObservedNodeDrain sets the last observed node drain in the status.
// It does not update the status.
// Returns true if the status was updated.
func setLastObservedNodeDrain(fd *managedupgradev1beta1.NodeForceDrain, nodeName, drainID string) bool {
	i := slices.IndexFunc(fd.Status.LastObservedNodeDrain, func(o managedupgradev1beta1.ObservedNodeDrain) bool {
		return o.NodeName == nodeName
	})
	if i < 0 {
		fd.Status.LastObservedNodeDrain = append(fd.Status.LastObservedNodeDrain, managedupgradev1beta1.ObservedNodeDrain{
			NodeName:         nodeName,
			LastAppliedDrain: drainID,
			ObservedTime:     metav1.Now(),
		})
		return true
	}
	if fd.Status.LastObservedNodeDrain[i].LastAppliedDrain == drainID {
		return false
	}
	fd.Status.LastObservedNodeDrain[i].LastAppliedDrain = drainID
	fd.Status.LastObservedNodeDrain[i].ObservedTime = metav1.Now()
	return true
}

func (r *NodeForceDrainReconciler) forceDrainNode(ctx context.Context, node corev1.Node) error {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.forceDrainNode").WithValues("node", node.Name)

	pods, err := r.getDeletionCandidatePodsForNode(ctx, node)
	if err != nil {
		return fmt.Errorf("failed to get deletion candidate pods for node %s: %w", node.Name, err)
	}

	deletionErrs := make([]error, 0, len(pods))
	for _, pod := range pods {
		l.Info("Deleting pod", "pod", pod.Name)
		if err := r.Delete(ctx, &pod); err != nil {
			deletionErrs = append(deletionErrs, err)
		}
	}

	return multierr.Combine(deletionErrs...)
}

func (r *NodeForceDrainReconciler) forceDeletePodsOnNode(ctx context.Context, node corev1.Node, gracePeriod time.Duration) error {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.forceDeletePodsOnNode").WithValues("node", node.Name)

	pods, err := r.getDeletionCandidatePodsForNode(ctx, node)
	if err != nil {
		return fmt.Errorf("failed to get deletion candidate pods for node %s: %w", node.Name, err)
	}

	deletionErrs := make([]error, 0, len(pods))
	for _, pod := range pods {
		if pod.DeletionTimestamp == nil {
			continue
		}
		if time.Now().Before(pod.DeletionTimestamp.Add(gracePeriod)) {
			l.Info("Pod is still in grace period", "pod", pod.Name, "deletionTimestamp", pod.DeletionTimestamp, "gracePeriod", gracePeriod)
		}

		l.Info("Force deleting pod", "pod", pod.Name)
		if err := r.Delete(ctx, &pod, &client.DeleteOptions{
			GracePeriodSeconds: ptr.To(int64(0)),
		}); err != nil {
			deletionErrs = append(deletionErrs, err)
		}
	}

	return multierr.Combine(deletionErrs...)
}

// getDeletionCandidatePodsForNode returns a list of pods on the node that are not controlled by an active DaemonSet.
// Pods controlled by an active DaemonSet are not returned.
// Only pod metadata is returned.
func (r *NodeForceDrainReconciler) getDeletionCandidatePodsForNode(ctx context.Context, node corev1.Node) ([]metav1.PartialObjectMetadata, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.getDeletionCandidatePodsForNode").WithValues("node", node.Name)

	var pods metav1.PartialObjectMetadataList
	pods.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))

	if err := r.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node.Name}); err != nil {
		return nil, fmt.Errorf("failed to list pods on node %s: %w", node.Name, err)
	}

	filteredPods := make([]metav1.PartialObjectMetadata, 0, len(pods.Items))
	for _, pod := range pods.Items {
		controlledByActiveDaemonSet, err := r.podIsControlledByActiveDaemonSet(ctx, pod)
		if err != nil {
			l.Error(err, "Failed to check if pod is controlled by active DaemonSet", "pod", pod.Name)
			continue
		}
		if controlledByActiveDaemonSet {
			l.Info("Pod is controlled by active DaemonSet. Skipping", "pod", pod.Name)
			continue
		}

		filteredPods = append(filteredPods, pod)
	}

	return filteredPods, nil
}

func (r *NodeForceDrainReconciler) podIsControlledByActiveDaemonSet(ctx context.Context, pod metav1.PartialObjectMetadata) (bool, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.podIsControlledByActiveDaemonSet")

	controllerRef := metav1.GetControllerOf(&pod)
	if controllerRef == nil {
		return false, nil
	}
	if controllerRef.APIVersion != "apps/v1" || controllerRef.Kind != "DaemonSet" {
		return false, nil
	}
	var ds metav1.PartialObjectMetadata
	ds.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("DaemonSet"))
	err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: controllerRef.Name}, &ds)
	if err != nil {
		// Edge case: Pod was orphaned
		// See https://github.com/kubernetes/kubectl/blob/442e3d141a35703b7637f41339b9f73cad005c47/pkg/drain/filters.go#L174
		if apierrors.IsNotFound(err) {
			l.Info("No daemon set found for pod", "daemonSet", controllerRef.Name, "pod", pod.Name, "namespace", pod.Namespace)
			return false, nil
		}
		return false, fmt.Errorf("failed to get DaemonSet %s: %w", controllerRef.Name, err)
	}
	return true, nil
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
