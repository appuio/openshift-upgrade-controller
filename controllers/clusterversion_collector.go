package controllers

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

var cvInfoDesc = prometheus.NewDesc(
	MetricsNamespace+"_cluster_version_info",
	"Managed ClusterVersion info metric. Shows the currently applied cluster version.",
	append([]string{"cluster_version"}, cvFields...),
	nil,
)

var cvOverlayDesc = prometheus.NewDesc(
	MetricsNamespace+"_cluster_version_overlay_timestamp_seconds",
	"Managed ClusterVersion info metric. Shows the fully merged cluster versions applied at their respective timestamps. The value is the timestamp in seconds since epoch.",
	append([]string{"cluster_version", "from"}, cvFields...),
	nil,
)

// ClusterVersionCollector is a Prometheus collector that collects cluster info metrics.
type ClusterVersionCollector struct {
	Client client.Client

	ManagedClusterVersionName      string
	ManagedClusterVersionNamespace string
}

var _ prometheus.Collector = &ClusterVersionCollector{}

// Describe implements prometheus.Collector.
// Sends the descriptors of the metrics to the channel.
func (*ClusterVersionCollector) Describe(d chan<- *prometheus.Desc) {
	d <- cvInfoDesc
	d <- cvOverlayDesc
}

// Collect implements prometheus.Collector.
// Collects metrics from the cluster version object and sends them to the channel.
func (m *ClusterVersionCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()

	var cv managedupgradev1beta1.ClusterVersion
	if err := m.Client.Get(ctx, types.NamespacedName{Name: m.ManagedClusterVersionName, Namespace: m.ManagedClusterVersionNamespace}, &cv); err != nil {
		err := fmt.Errorf("failed to get managedupgrade.ClusterVersion: %w", err)
		ch <- prometheus.NewInvalidMetric(cvInfoDesc, err)
		ch <- prometheus.NewInvalidMetric(cvOverlayDesc, err)
		return
	}

	cvFields := extractCVFields(cv.Status.Current)
	ch <- prometheus.MustNewConstMetric(
		cvInfoDesc,
		prometheus.GaugeValue,
		1,
		append([]string{cv.Name}, cvFields...)...,
	)

	for _, overlay := range cv.Status.Overlays {
		cvFields := extractCVFields(overlay.Preview.Spec)
		ch <- prometheus.MustNewConstMetric(
			cvOverlayDesc,
			prometheus.GaugeValue,
			float64(overlay.From.Time.Unix()),
			append([]string{cv.Name, overlay.From.Time.UTC().Format(time.RFC3339)}, cvFields...)...,
		)
	}
}

var cvFields = []string{
	"baseline_capability_set",
	"channel",
	"cluster_id",
	"upstream",
}

func extractCVFields(cvs configv1.ClusterVersionSpec) []string {
	blCap := ""
	if cvs.Capabilities != nil {
		blCap = string(cvs.Capabilities.BaselineCapabilitySet)
	}
	return []string{
		blCap,
		cvs.Channel,
		string(cvs.ClusterID),
		string(cvs.Upstream),
	}
}
