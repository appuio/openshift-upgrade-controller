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

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var machineInfoDesc = prometheus.NewDesc(
	MetricsNamespace+"_machine_info",
	"OpenShift Machine information",
	[]string{
		"machine",
		"machineset",
		"node_ref",
		"provider_id",
		"phase",
	},
	nil,
)

// MachineCollector collects metrics from Machines
type MachineCollector struct {
	client.Client

	// MachineNamespace is the namespace where the machines are located.
	MachineNamespace string
}

//+kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch

var _ prometheus.Collector = &MachineCollector{}

// Describe implements prometheus.Collector.
// Sends the static description of the metrics to the provided channel.
func (*MachineCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- machineInfoDesc
}

// Collect implements prometheus.Collector.
// Sends a metric with the current state of the machines to the provided channel.
func (c *MachineCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()

	var machines machinev1beta1.MachineList
	if err := c.Client.List(ctx, &machines, client.InNamespace(c.MachineNamespace)); err != nil {
		err := fmt.Errorf("failed list to machines: %w", err)
		ch <- prometheus.NewInvalidMetric(machineInfoDesc, err)
	}

	for _, machine := range machines.Items {
		var machineSetName string
		if controllerRef := metav1.GetControllerOf(&machine); controllerRef != nil && controllerRef.Kind == "MachineSet" {
			machineSetName = controllerRef.Name
		}

		ch <- prometheus.MustNewConstMetric(
			machineInfoDesc,
			prometheus.GaugeValue,
			1,
			machine.Name,
			machineSetName,
			ptr.Deref(machine.Status.NodeRef, corev1.ObjectReference{}).Name,
			ptr.Deref(machine.Spec.ProviderID, ""),
			ptr.Deref(machine.Status.Phase, ""),
		)
	}
}
