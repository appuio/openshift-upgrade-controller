package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const MetricsNamespace = "openshift_upgrade_controller"

var (
	nodeDraining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Name:      "node_draining",
			Help:      "Node draining status",
		},
		[]string{"node"},
	)
)

func init() {
	metrics.Registry.MustRegister(nodeDraining)
}
