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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// https://github.com/openshift/machine-config-operator/blob/b36482885ba1304e122e7c01c26cd671dfdd0418/pkg/daemon/constants/constants.go#L17
	// https://github.com/openshift/machine-config-operator/blob/b36482885ba1304e122e7c01c26cd671dfdd0418/pkg/daemon/drain.go#L79
	// DesiredDrainerAnnotationKey is set by OCP to indicate drain/uncordon requests
	DesiredDrainerAnnotationKey = "machineconfiguration.openshift.io/desiredDrain"
	// LastAppliedDrainerAnnotationKey is by OCP to indicate the last request applied
	LastAppliedDrainerAnnotationKey = "machineconfiguration.openshift.io/lastAppliedDrain"
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile reacts to Node changes and updates the node draining metric.
func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		nodeDraining.DeleteLabelValues(req.Name)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !node.DeletionTimestamp.IsZero() {
		nodeDraining.DeleteLabelValues(node.Name)
		return ctrl.Result{}, nil
	}

	desiredDrain := node.Annotations[DesiredDrainerAnnotationKey]
	lastAppliedDrain := node.Annotations[LastAppliedDrainerAnnotationKey]

	if desiredDrain == lastAppliedDrain {
		nodeDraining.WithLabelValues(node.Name).Set(0)
		return ctrl.Result{}, nil
	}

	nodeDraining.WithLabelValues(node.Name).Set(1)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}
