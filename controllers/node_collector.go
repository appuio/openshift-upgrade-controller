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

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
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

var nodeDrainingDesc = prometheus.NewDesc(
	MetricsNamespace+"_node_draining",
	"Node draining status",
	[]string{
		"node",
	},
	nil,
)

// NodeCollector collects metrics from Nodes
type NodeCollector struct {
	client.Client
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

var _ prometheus.Collector = &NodeCollector{}

// Describe implements prometheus.Collector.
// Sends the static description of the metrics to the provided channel.
func (*NodeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- nodeDrainingDesc
}

// Collect implements prometheus.Collector.
// Sends a metric with the current value of the Node draining status to the provided channel.
func (c *NodeCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()

	var nodes corev1.NodeList
	if err := c.Client.List(ctx, &nodes); err != nil {
		err := fmt.Errorf("failed list to nodes: %w", err)
		ch <- prometheus.NewInvalidMetric(nodeDrainingDesc, err)
	}

	for _, node := range nodes.Items {
		ch <- prometheus.MustNewConstMetric(
			nodeDrainingDesc,
			prometheus.GaugeValue,
			boolToFloat64(node.Annotations[DesiredDrainerAnnotationKey] != node.Annotations[LastAppliedDrainerAnnotationKey]),
			node.Name,
		)
	}
}
