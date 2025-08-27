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
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/itchyny/gojq"
	"go.uber.org/multierr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

// NodeForceDrainReconciler reconciles the NodeForceDrain object
// It should be called on node change events.
// It force drains nodes after a certain period of time.
type NodeForceDrainReconciler struct {
	client.Client
	// APIReader is used to read objects directly from the API.
	// It is used to avoid caching all pods in the controller.
	APIReader client.Reader
	Scheme    *runtime.Scheme

	// Recorder is used to record pod deletion events on nodes.
	Recorder record.EventRecorder

	Clock Clock

	// MaxReconcileIntervalDuringActiveDrain is the longest possible interval at which the controller reconciles during active node drains.
	// It is used to guard against edge cases where the controller might miss a pod deletion.
	// One example would be if a daemon set orphans a pod after the controller has reconciled.
	// It will also guard against any logic errors in the controller.
	MaxReconcileIntervalDuringActiveDrain time.Duration
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups="apps",resources=daemonsets,verbs=get;list;watch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=nodeforcedrains,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=managedupgrade.appuio.io,resources=nodeforcedrains/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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

	var nodes corev1.NodeList
	nodeSel, err := metav1.LabelSelectorAsSelector(&fd.Spec.NodeSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse NodeSelector: %w", err)
	}
	if err := r.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: nodeSel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	podNSSelector, err := metav1.LabelSelectorAsSelector(&fd.Spec.NamespaceSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse NamespaceSelector: %w", err)
	}

	var podJQQuery *gojq.Query
	if fd.Spec.PodJQSelector != "" {
		q, err := gojq.Parse(fd.Spec.PodJQSelector)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse PodJQSelector: %w", err)
		}
		podJQQuery = q
	}

	// Check for draining nodes, using the node drainer annotations.
	drainingNodes := make([]corev1.Node, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		desiredDrain := node.Annotations[DesiredDrainerAnnotationKey]
		lastAppliedDrain := node.Annotations[LastAppliedDrainerAnnotationKey]
		if desiredDrain == lastAppliedDrain {
			continue
		}
		drainingNodes = append(drainingNodes, node)
	}
	if len(drainingNodes) > 0 {
		nodeNames := make([]string, 0, len(drainingNodes))
		for _, node := range drainingNodes {
			nodeNames = append(nodeNames, node.Name)
		}
		l.Info("Found draining nodes", "nodes", nodeNames)
	}

	// Update the last observed node drains for nodes that started draining.
	statusChanged := false
	for _, node := range drainingNodes {
		statusChanged = statusChanged || r.setLastObservedNodeDrain(&fd, node.Name, node.Annotations[LastAppliedDrainerAnnotationKey])
	}
	if statusChanged {
		l.Info("Observed new node drain timestamps", "lastObservedNodeDrains", fd.Status.LastObservedNodeDrain)
		if err := r.Status().Update(ctx, &fd); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update LastObservedNodeDrain status: %w", err)
		}
		// We can return early here because the status update will trigger a new reconcile.
		return ctrl.Result{}, nil
	}

	// Check if any nodes are out of the grace period.
	var timeUntilNextDrain time.Duration
	nodesOutOfGracePeriod := make([]corev1.Node, 0, len(drainingNodes))
	for _, node := range drainingNodes {
		ld, found := findLastObservedNodeDrain(fd.Status, node.Name)
		if !found {
			l.Info("Node was not found in status. Skipping", "node", node.Name)
			continue
		}

		timeUntilDrain := ld.ObservedTime.Time.Sub(r.Clock.Now()) + fd.Spec.NodeDrainGracePeriod.Duration
		if timeUntilDrain > 0 {
			if timeUntilNextDrain == 0 || timeUntilDrain < timeUntilNextDrain {
				timeUntilNextDrain = timeUntilDrain
			}
			l.Info("Node is still in grace period", "node", node.Name, "lastObservedNodeDrain", ld, "nodeDrainGracePeriod", fd.Spec.NodeDrainGracePeriod, "timeUntilDrain", timeUntilDrain)
			continue
		}
		l.Info("Node is out of grace period", "node", node.Name, "lastObservedNodeDrain", ld, "nodeDrainGracePeriod", fd.Spec.NodeDrainGracePeriod)
		nodesOutOfGracePeriod = append(nodesOutOfGracePeriod, node)
	}

	// Force drain nodes by deleting all pods on the node.
	var forceDrainError error
	var didDeletePod bool
	if fd.Spec.NodeDrainGracePeriod.Duration != 0 {
		errors := make([]error, 0, len(nodesOutOfGracePeriod))
		for _, node := range nodesOutOfGracePeriod {
			l.Info("Force draining node", "node", node.Name)
			deleted, err := r.forceDrainNode(ctx, node, podNSSelector, podJQQuery)
			if deleted {
				didDeletePod = true
			}
			if err != nil {
				errors = append(errors, fmt.Errorf("failed to force drain node %s: %w", node.Name, err))
			}
		}
		forceDrainError = multierr.Combine(errors...)
	}

	// Force delete pods on nodes that are out of the grace period.
	var timeUntilNextPodForceDelete time.Duration
	var forceDeleteError error
	if pfd := fd.Spec.PodForceDeleteGracePeriod.Duration; pfd != 0 {
		errors := make([]error, 0, len(nodesOutOfGracePeriod))
		for _, node := range nodesOutOfGracePeriod {
			l.Info("Force deleting pods on node", "node", node.Name)
			if t, err := r.forceDeletePodsOnNode(ctx, node, pfd, podNSSelector, podJQQuery); err != nil {
				errors = append(errors, fmt.Errorf("failed to force delete pods on node %s: %w", node.Name, err))
			} else if t > 0 && (timeUntilNextPodForceDelete == 0 || t < timeUntilNextPodForceDelete) {
				timeUntilNextPodForceDelete = t
			}
		}
		forceDeleteError = multierr.Combine(errors...)
	}

	if err := multierr.Combine(forceDrainError, forceDeleteError); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue after the time until the next timeout.
	// This timeout can be due to node grace periods or pod force delete grace periods.
	requeueAfter := timeUntilNextDrain
	if didDeletePod && fd.Spec.PodForceDeleteGracePeriod.Duration != 0 && (requeueAfter == 0 || fd.Spec.PodForceDeleteGracePeriod.Duration < requeueAfter) {
		requeueAfter = fd.Spec.PodForceDeleteGracePeriod.Duration
	}
	if timeUntilNextPodForceDelete > 0 && (requeueAfter == 0 || timeUntilNextPodForceDelete < requeueAfter) {
		requeueAfter = timeUntilNextPodForceDelete
	}
	// Ensure we respect the maximum reconcile interval during active node drains.
	if len(drainingNodes) > 0 && r.MaxReconcileIntervalDuringActiveDrain > 0 && (requeueAfter == 0 || requeueAfter > r.MaxReconcileIntervalDuringActiveDrain) {
		requeueAfter = r.MaxReconcileIntervalDuringActiveDrain
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
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
func (r *NodeForceDrainReconciler) setLastObservedNodeDrain(fd *managedupgradev1beta1.NodeForceDrain, nodeName, drainID string) bool {
	i := slices.IndexFunc(fd.Status.LastObservedNodeDrain, func(o managedupgradev1beta1.ObservedNodeDrain) bool {
		return o.NodeName == nodeName
	})
	if i < 0 {
		fd.Status.LastObservedNodeDrain = append(fd.Status.LastObservedNodeDrain, managedupgradev1beta1.ObservedNodeDrain{
			NodeName:         nodeName,
			LastAppliedDrain: drainID,
			ObservedTime:     metav1.Time{Time: r.Clock.Now()},
		})
		return true
	}
	if fd.Status.LastObservedNodeDrain[i].LastAppliedDrain == drainID {
		return false
	}
	fd.Status.LastObservedNodeDrain[i].LastAppliedDrain = drainID
	fd.Status.LastObservedNodeDrain[i].ObservedTime = metav1.Time{Time: r.Clock.Now()}
	return true
}

// forceDrainNode deletes all pods on the node.
// Returns true if there was an attempt to delete a pod and any errors that occurred.
func (r *NodeForceDrainReconciler) forceDrainNode(ctx context.Context, node corev1.Node, podNSSelector labels.Selector, podJQQuery *gojq.Query) (bool, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.forceDrainNode").WithValues("node", node.Name)

	pods, err := r.getDeletionCandidatePodsForNode(ctx, node, podNSSelector, podJQQuery)
	if err != nil {
		return false, fmt.Errorf("failed to get deletion candidate pods for node %s: %w", node.Name, err)
	}

	var attemptedDeletion bool
	deletionErrs := make([]error, 0, len(pods))
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		l.Info("Deleting pod", "pod", pod.Name, "podNamespace", pod.Namespace)
		attemptedDeletion = true
		r.Recorder.Eventf(&node, corev1.EventTypeWarning, "NodeDrainDeletePod", "Deleting pod %q in namespace %q", pod.Name, pod.Namespace)
		if err := r.Delete(ctx, &pod); err != nil {
			deletionErrs = append(deletionErrs, err)
		}
	}

	return attemptedDeletion, multierr.Combine(deletionErrs...)
}

// forceDeletePodsOnNode deletes all pods on the node.
// Returns the time until the next force delete and any errors that occurred.
// A time of 0 means that there are no pods to force delete.
func (r *NodeForceDrainReconciler) forceDeletePodsOnNode(ctx context.Context, node corev1.Node, gracePeriod time.Duration, podNSSelector labels.Selector, podJQQuery *gojq.Query) (time.Duration, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.forceDeletePodsOnNode").WithValues("node", node.Name)

	pods, err := r.getDeletionCandidatePodsForNode(ctx, node, podNSSelector, podJQQuery)
	if err != nil {
		return 0, fmt.Errorf("failed to get deletion candidate pods for node %s: %w", node.Name, err)
	}

	var timeUntilNextForceDelete time.Duration
	deletionErrs := make([]error, 0, len(pods))
	for _, pod := range pods {
		l := l.WithValues("pod", pod.Name, "podNamespace", pod.Namespace)
		if pod.DeletionTimestamp == nil {
			continue
		}
		// Pod deletion timestamp is in the future and includes the grace period.
		podGracePeriod := time.Duration(ptr.Deref(pod.ObjectMeta.DeletionGracePeriodSeconds, 0)) * time.Second
		timeUntilForceDelete := pod.DeletionTimestamp.Sub(r.Clock.Now()) - podGracePeriod + gracePeriod
		if timeUntilForceDelete > 0 {
			if timeUntilNextForceDelete == 0 || timeUntilForceDelete < timeUntilNextForceDelete {
				timeUntilNextForceDelete = timeUntilForceDelete
			}
			l.Info("Pod is still in grace period", "deletionTimestamp", pod.DeletionTimestamp, "gracePeriod", gracePeriod, "timeUntilForceDelete", timeUntilForceDelete)
			continue
		}

		l.Info("Forcing pod termination")
		r.Recorder.Eventf(&node, corev1.EventTypeWarning, "NodeDrainForcingPodTermination", "Forcing pod termination: Deleting pod %q in namespace %q with grace period of 1.", pod.Name, pod.Namespace)
		if err := r.Delete(ctx, &pod, &client.DeleteOptions{
			// As far is I was able to find a grace period of 0 will leave the hanging pod on the node and block the reboot of the node.
			// Therefore we set a grace period of 1 second for the quickest possible deletion.
			// See kubectl delete docs:
			// https://kubernetes.io/docs/reference/generated/kubectl/kubectl-commands#delete
			// > Period of time in seconds given to the resource to terminate gracefully. [...] Set to 1 for immediate shutdown. Can only be set to 0 when --force is true (force deletion).
			GracePeriodSeconds: ptr.To(int64(1)),
		}); err != nil {
			deletionErrs = append(deletionErrs, err)
		}
	}

	return timeUntilNextForceDelete, multierr.Combine(deletionErrs...)
}

// getDeletionCandidatePodsForNode returns a list of pods on the node that are not controlled by an active DaemonSet.
// Pods controlled by an active DaemonSet are not returned.
func (r *NodeForceDrainReconciler) getDeletionCandidatePodsForNode(ctx context.Context, node corev1.Node, podNSSelector labels.Selector, podJQQuery *gojq.Query) ([]corev1.Pod, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.getDeletionCandidatePodsForNode").WithValues("node", node.Name)

	// We use the API directly to avoid caching all pods in the controller.
	var pods corev1.PodList
	if err := r.APIReader.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node.Name}); err != nil {
		return nil, fmt.Errorf("failed to list pods on node %s: %w", node.Name, err)
	}

	type ignoredPod struct {
		Name      string
		Namespace string
		Reason    string
	}

	ignoredPods := make([]ignoredPod, 0, len(pods.Items))
	filteredPods := make([]corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		l := l.WithValues("pod", pod.Name, "podNamespace", pod.Namespace)
		ctx := log.IntoContext(ctx, l)

		inMatchingNs, err := r.podIsInMatchingNamespace(ctx, pod, podNSSelector)
		if err != nil {
			ignoredPods = append(ignoredPods, ignoredPod{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Reason:    "error checking namespace",
			})
			continue
		}
		if !inMatchingNs {
			ignoredPods = append(ignoredPods, ignoredPod{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Reason:    "namespace does not match",
			})
			continue
		}

		if podJQQuery != nil {
			matches, err := r.podJQQueryMatches(ctx, pod, podJQQuery)
			if err != nil {
				ignoredPods = append(ignoredPods, ignoredPod{
					Name:      pod.Name,
					Namespace: pod.Namespace,
					Reason:    "error evaluating JQ query",
				})
				continue
			}
			if !matches {
				ignoredPods = append(ignoredPods, ignoredPod{
					Name:      pod.Name,
					Namespace: pod.Namespace,
					Reason:    "pod does not match JQ query",
				})
				continue
			}
		}

		controlledByActiveDaemonSet, err := r.podIsControlledByExistingDaemonSet(ctx, pod)
		if err != nil {
			l.Error(err, "Failed to check if pod is controlled by active DaemonSet")
			ignoredPods = append(ignoredPods, ignoredPod{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Reason:    "error checking daemonset",
			})
			continue
		}
		if controlledByActiveDaemonSet {
			ignoredPods = append(ignoredPods, ignoredPod{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Reason:    "controlled by daemonset",
			})
			continue
		}
		if r.podIsStatic(pod) {
			ignoredPods = append(ignoredPods, ignoredPod{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Reason:    "static pod",
			})
			continue
		}

		filteredPods = append(filteredPods, pod)
	}

	if len(ignoredPods) > 0 {
		l.Info("Ignoring pods", "ignoredPods", ignoredPods)
	}

	return filteredPods, nil
}

func (r *NodeForceDrainReconciler) podIsInMatchingNamespace(ctx context.Context, pod corev1.Pod, podNSSelector labels.Selector) (bool, error) {
	if podNSSelector.Empty() {
		return true, nil
	}

	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: pod.Namespace}, &ns); err != nil {
		return false, fmt.Errorf("failed to get namespace for pod %s: %w", pod.Name, err)
	}

	return podNSSelector.Matches(labels.Set(ns.Labels)), nil
}

func (r *NodeForceDrainReconciler) podJQQueryMatches(ctx context.Context, pod corev1.Pod, podJQQuery *gojq.Query) (bool, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.podJQQueryMatches")

	podBytes, err := json.Marshal(pod)
	if err != nil {
		return false, fmt.Errorf("failed to convert pod to raw JSON (marshal): %w", err)
	}
	var rawPod any
	if err := json.Unmarshal(podBytes, &rawPod); err != nil {
		return false, fmt.Errorf("failed to convert pod to raw JSON (unmarshal): %w", err)
	}

	matches := false
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	iter := podJQQuery.RunWithContext(runCtx, rawPod)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			if err, ok := err.(*gojq.HaltError); ok && err.Value() == nil {
				break
			}
			l.Error(err, "JQ query returned error, halting")
			break
		}
		if vb, ok := v.(bool); ok {
			matches = matches || vb
		} else {
			l.Error(fmt.Errorf("JQ query did not return a boolean, returned %T (%+v)", v, v), "Ignore query result")
		}
	}
	return matches, nil
}

// podIsStatic returns true if the pod is a static pod.
// https://kubernetes.io/docs/tasks/configure-pod-container/static-pod/
// Such pods are directly managed by the kubelet and should not be deleted.
// The check is based on the pod's controller being the node itself.
func (r *NodeForceDrainReconciler) podIsStatic(pod corev1.Pod) bool {
	controllerRef := metav1.GetControllerOf(&pod)
	if controllerRef == nil {
		return false
	}

	return controllerRef.APIVersion == "v1" && controllerRef.Kind == "Node"
}

// podIsControlledByExistingDaemonSet returns true if the pod is controlled by an existing DaemonSet.
// This is determined by checking if the pod's controller is a DaemonSet and if the DaemonSet exists in the API.
func (r *NodeForceDrainReconciler) podIsControlledByExistingDaemonSet(ctx context.Context, pod corev1.Pod) (bool, error) {
	l := log.FromContext(ctx).WithName("NodeForceDrainReconciler.podIsControlledByActiveDaemonSet")

	controllerRef := metav1.GetControllerOf(&pod)
	if controllerRef == nil {
		return false, nil
	}
	if controllerRef.APIVersion != "apps/v1" || controllerRef.Kind != "DaemonSet" {
		return false, nil
	}
	// We don't need a full object, metadata is enough.
	var ds metav1.PartialObjectMetadata
	ds.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("DaemonSet"))
	err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: controllerRef.Name}, &ds)
	if err != nil {
		// Edge case: Pod was orphaned
		// See https://github.com/kubernetes/kubectl/blob/442e3d141a35703b7637f41339b9f73cad005c47/pkg/drain/filters.go#L174
		if apierrors.IsNotFound(err) {
			l.Info("No daemon set found for pod", "daemonSet", controllerRef.Name, "pod", pod.Name, "podNamespace", pod.Namespace)
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

			sel, err := metav1.LabelSelectorAsSelector(&fd.Spec.NodeSelector)
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
