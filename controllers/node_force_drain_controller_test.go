package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_NodeForceDrainReconciler_Reconcile_E2E(t *testing.T) {
	ctx := log.IntoContext(context.Background(), testr.New(t))

	clock := mockClock{now: time.Date(2022, time.April, 4, 8, 0, 0, 0, time.Local)}
	t.Log("Now: ", clock.Now())

	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"node-role.kubernetes.io/app": "",
			},
			Annotations: map[string]string{
				LastAppliedDrainerAnnotationKey: "node1-drainer-1",
				DesiredDrainerAnnotationKey:     "node1-drainer-1",
			},
		},
	}
	podOnNode1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-on-node1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: node1.Name,
		},
	}
	podToForceDeleteOnNode1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pod-on-node1-force-delete",
			Namespace:  "default",
			Finalizers: []string{StuckPodSimulationFinalizer},
		},
		Spec: corev1.PodSpec{
			NodeName: node1.Name,
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node2",
			Labels: node1.Labels,
			Annotations: map[string]string{
				LastAppliedDrainerAnnotationKey: "node2-drainer-1",
				DesiredDrainerAnnotationKey:     "node2-drainer-1",
			},
		},
	}
	drainingStorageNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "draining-storage-node",
			Labels: map[string]string{
				"node-role.kubernetes.io/storage": "",
			},
			Annotations: map[string]string{
				LastAppliedDrainerAnnotationKey: "draining-storage-node-drainer-1",
				DesiredDrainerAnnotationKey:     "draining-storage-node-drainer-2",
			},
		},
	}
	stuckPodOnDrainingStorageNode := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stuck-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: drainingStorageNode.Name,
		},
	}

	forceDrain := &managedupgradev1beta1.NodeForceDrain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-force-drain",
			Namespace: "default",
		},
		Spec: managedupgradev1beta1.NodeForceDrainSpec{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: node1.Labels,
			},
			NodeDrainGracePeriod:      metav1.Duration{Duration: 1 * time.Hour},
			PodForceDeleteGracePeriod: metav1.Duration{Duration: 15 * time.Minute},
		},
	}

	cli := controllerClient(t, node1, node2, drainingStorageNode, stuckPodOnDrainingStorageNode, forceDrain, podOnNode1, podToForceDeleteOnNode1)

	subject := &NodeForceDrainReconciler{
		Client:    cli,
		APIReader: cli,
		Scheme:    cli.Scheme(),

		Clock: &clock,
	}

	step(t, "initial reconcile", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter)

		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(forceDrain), forceDrain))
		require.Len(t, forceDrain.Status.LastObservedNodeDrain, 0)
	})

	step(t, "node1 starts draining", func(t *testing.T) {
		node1.Annotations[DesiredDrainerAnnotationKey] = "node1-drainer-2"
		require.NoError(t, cli.Update(ctx, node1))

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter)

		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(forceDrain), forceDrain))
		require.Len(t, forceDrain.Status.LastObservedNodeDrain, 1, "controller should have one observed node drain")
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[0].NodeName, node1.Name)
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[0].LastAppliedDrain, "node1-drainer-1")
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[0].ObservedTime.Time, clock.Now())
	})

	step(t, "time advances, still in grace period", func(t *testing.T) {
		clock.Advance(30 * time.Minute)

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Equal(t, 30*time.Minute, res.RequeueAfter)
	})

	step(t, "second node starts draining", func(t *testing.T) {
		node2.Annotations[DesiredDrainerAnnotationKey] = "node2-drainer-2"
		require.NoError(t, cli.Update(ctx, node2))

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter, "controller updates status so no need to requeue")

		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(forceDrain), forceDrain))
		require.Len(t, forceDrain.Status.LastObservedNodeDrain, 2, "controller should have two observed node drains")
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[1].NodeName, node2.Name)
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[1].LastAppliedDrain, "node2-drainer-1")
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[1].ObservedTime.Time, clock.Now())
	})

	step(t, "time advances, node1 out of grace period", func(t *testing.T) {
		clock.Advance(30 * time.Minute)

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Equal(t, forceDrain.Spec.PodForceDeleteGracePeriod.Duration, res.RequeueAfter, "freshly deleted pods might need a second hammer")

		var deletedPod corev1.Pod
		require.Error(t, cli.Get(ctx, client.ObjectKeyFromObject(podOnNode1), &deletedPod), "pod should have been deleted")
	})

	t.Log("We can't control the clock used for DeletionTimestamp so there's a sequence break here. We will test force deletion at the end by skipping to real time.")

	step(t, "time advances, both nodes out of grace period, time advances to end of force deletion grace period", func(t *testing.T) {
		clock.Advance(30 * time.Minute)

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.NotZero(t, res.RequeueAfter, "controller should requeue to check for stuck pods, since we can't control DeletionTimestamps they are and the future and the requeue is for into the future")

		clock.Advance(res.RequeueAfter)
	})

	step(t, "stuck pod on draining node gets force deleted", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter)

		var deletedPod corev1.Pod
		require.Error(t, cli.Get(ctx, client.ObjectKeyFromObject(podToForceDeleteOnNode1), &deletedPod), "pod should have been deleted")
	})

	step(t, "nodes stop draining", func(t *testing.T) {
		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(node1), node1))
		node1.Annotations[LastAppliedDrainerAnnotationKey] = node1.Annotations[DesiredDrainerAnnotationKey]
		require.NoError(t, cli.Update(ctx, node1))

		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(node2), node2))
		node2.Annotations[LastAppliedDrainerAnnotationKey] = node2.Annotations[DesiredDrainerAnnotationKey]
		require.NoError(t, cli.Update(ctx, node2))

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter, "no requeue needed, next node drain will trigger reconcile")
	})

	step(t, "node1 starts draining again", func(t *testing.T) {
		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(node1), node1))
		node1.Annotations[DesiredDrainerAnnotationKey] = "node1-drainer-3"
		require.NoError(t, cli.Update(ctx, node1))

		res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
		require.NoError(t, err)
		require.Zero(t, res.RequeueAfter)

		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(forceDrain), forceDrain))
		require.Len(t, forceDrain.Status.LastObservedNodeDrain, 2, "controller should still have two observed node drains")
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[0].NodeName, node1.Name)
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[0].LastAppliedDrain, "node1-drainer-2")
		require.Equal(t, forceDrain.Status.LastObservedNodeDrain[0].ObservedTime.Time, clock.Now())
	})

	step(t, "control that the storage node pod was never deleted", func(t *testing.T) {
		var storageNodePod corev1.Pod
		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(stuckPodOnDrainingStorageNode), &storageNodePod), "pod should still be there")
	})
}

func Test_NodeForceDrainReconciler_Reconcile_DrainIgnoreActiveDaemonsSetsStaticPods(t *testing.T) {
	ctx := log.IntoContext(context.Background(), testr.New(t))

	clock := mockClock{now: time.Date(2022, time.April, 4, 8, 0, 0, 0, time.Local)}
	t.Log("Now: ", clock.Now())

	drainingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"node-role.kubernetes.io/app": "",
			},
			Annotations: map[string]string{
				LastAppliedDrainerAnnotationKey: "node1-drainer-1",
				DesiredDrainerAnnotationKey:     "node1-drainer-2",
			},
		},
	}
	podWithNoController := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-no-controller",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: drainingNode.Name,
		},
	}
	podWithDeploymentController := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-deployment-controller",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment",
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: drainingNode.Name,
		},
	}
	ownerDaemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "daemonset",
			Namespace: "default",
		},
	}
	podWithActiveDaemonSetController := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-active-daemonset-controller",
			Namespace: ownerDaemonSet.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       ownerDaemonSet.Name,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: drainingNode.Name,
		},
	}
	podWithInvalidDaemonSetController := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-invalid-daemonset-controller",
			Namespace: ownerDaemonSet.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "other-daemonset",
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: drainingNode.Name,
		},
	}
	staticPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "static-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Node",
					Name:       drainingNode.Name,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: drainingNode.Name,
		},
	}

	forceDrain := &managedupgradev1beta1.NodeForceDrain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-force-drain",
			Namespace: "default",
		},
		Spec: managedupgradev1beta1.NodeForceDrainSpec{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: drainingNode.Labels,
			},
			NodeDrainGracePeriod: metav1.Duration{Duration: 1 * time.Hour},
		},
		Status: managedupgradev1beta1.NodeForceDrainStatus{
			LastObservedNodeDrain: []managedupgradev1beta1.ObservedNodeDrain{
				{
					NodeName:         drainingNode.Name,
					LastAppliedDrain: "node1-drainer-1",
					ObservedTime:     metav1.NewTime(clock.Now().Add(-5 * time.Hour)),
				},
			},
		},
	}

	cli := controllerClient(t, forceDrain, drainingNode,
		podWithNoController, podWithDeploymentController, ownerDaemonSet, podWithActiveDaemonSetController, podWithInvalidDaemonSetController, staticPod)

	subject := &NodeForceDrainReconciler{
		Client:    cli,
		APIReader: cli,
		Scheme:    cli.Scheme(),

		Clock: &clock,
	}

	var pods corev1.PodList
	require.NoError(t, cli.List(ctx, &pods, client.InNamespace("default")))
	require.Len(t, pods.Items, 5, "precondition: all testing pods should be present")

	_, err := subject.Reconcile(ctx, requestForObject(forceDrain))
	require.NoError(t, err)

	var podsAfterDrain corev1.PodList
	require.NoError(t, cli.List(ctx, &podsAfterDrain, client.InNamespace("default")))
	podNames := make([]string, 0, len(podsAfterDrain.Items))
	for _, pod := range podsAfterDrain.Items {
		podNames = append(podNames, pod.Name)
	}
	require.ElementsMatch(t, podNames, []string{"pod-with-active-daemonset-controller", "static-pod"}, "the pod with an active DaemonSet controller and the static pod should be left alone")
}

func Test_NodeForceDrainReconciler_Reconcile_MaxIntervalDuringActiveDrain(t *testing.T) {
	ctx := log.IntoContext(context.Background(), testr.New(t))

	clock := mockClock{now: time.Date(2022, time.April, 4, 8, 0, 0, 0, time.Local)}
	t.Log("Now: ", clock.Now())

	drainingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"node-role.kubernetes.io/app": "",
			},
			Annotations: map[string]string{
				LastAppliedDrainerAnnotationKey: "node1-drainer-1",
				DesiredDrainerAnnotationKey:     "node1-drainer-2",
			},
		},
	}

	forceDrain := &managedupgradev1beta1.NodeForceDrain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-force-drain",
			Namespace: "default",
		},
		Spec: managedupgradev1beta1.NodeForceDrainSpec{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: drainingNode.Labels,
			},
			NodeDrainGracePeriod: metav1.Duration{Duration: 1 * time.Hour},
		},
		Status: managedupgradev1beta1.NodeForceDrainStatus{
			LastObservedNodeDrain: []managedupgradev1beta1.ObservedNodeDrain{
				{
					NodeName:         drainingNode.Name,
					LastAppliedDrain: "node1-drainer-1",
					ObservedTime:     metav1.NewTime(clock.Now().Add(-10 * time.Minute)),
				},
			},
		},
	}

	cli := controllerClient(t, forceDrain, drainingNode)

	subject := &NodeForceDrainReconciler{
		Client:    cli,
		APIReader: cli,
		Scheme:    cli.Scheme(),

		Clock: &clock,
	}

	res, err := subject.Reconcile(ctx, requestForObject(forceDrain))
	require.NoError(t, err)
	require.Equal(t, 50*time.Minute, res.RequeueAfter, "requeue after grace period")

	subject.MaxReconcileIntervalDuringActiveDrain = 3 * time.Minute
	res, err = subject.Reconcile(ctx, requestForObject(forceDrain))
	require.NoError(t, err)
	require.Equal(t, 3*time.Minute, res.RequeueAfter, "requeue after max interval")

	clock.Advance(49 * time.Minute)
	res, err = subject.Reconcile(ctx, requestForObject(forceDrain))
	require.NoError(t, err)
	require.Equal(t, 1*time.Minute, res.RequeueAfter, "requeue after grace period if max interval is not reached")

	drainingNode.Annotations[LastAppliedDrainerAnnotationKey] = drainingNode.Annotations[DesiredDrainerAnnotationKey]
	require.NoError(t, cli.Update(ctx, drainingNode))

	res, err = subject.Reconcile(ctx, requestForObject(forceDrain))
	require.NoError(t, err)
	require.Zero(t, res.RequeueAfter, "max interval should not apply if node is actively draining")
}

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
