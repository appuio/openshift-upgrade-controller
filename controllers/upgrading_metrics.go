package controllers

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
	"github.com/appuio/openshift-upgrade-controller/pkg/healthcheck"
)

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch

var clusterUpgradingDesc = prometheus.NewDesc(
	MetricsNamespace+"_cluster_upgrading",
	"Set to 1 if the cluster is currently upgrading, 0 otherwise.",
	[]string{},
	nil,
)

var poolsUpgradingDesc = prometheus.NewDesc(
	MetricsNamespace+"_machine_config_pools_upgrading",
	"Set to 1 if a machine config pool in the cluster is currently upgrading, 0 otherwise.",
	[]string{"pool"},
	nil,
)

var jobStates = prometheus.NewDesc(
	MetricsNamespace+"_upgradejob_state",
	"Returns the state of jobs in the cluster. 'pending', 'active', 'succeeded', or 'failed' are possible states.",
	[]string{"upgradejob", "state"},
	nil,
)

// ClusterUpgradingMetric is a Prometheus collector that exposes the link between an organization and a billing entity.
type ClusterUpgradingMetric struct {
	client.Client

	ManagedUpstreamClusterVersionName string
}

var _ prometheus.Collector = &ClusterUpgradingMetric{}

// Describe implements prometheus.Collector.
// Sends the static description of the metrics to the provided channel.
func (*ClusterUpgradingMetric) Describe(ch chan<- *prometheus.Desc) {
	ch <- clusterUpgradingDesc
	ch <- poolsUpgradingDesc
	ch <- jobStates
}

// Collect implements prometheus.Collector.
// Sends a metric if the cluster is currently upgrading and a upgrading metric for each machine config pool.
func (m *ClusterUpgradingMetric) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()

	mcpl := machineconfigurationv1.MachineConfigPoolList{}
	if err := m.Client.List(ctx, &mcpl); err != nil {
		err := fmt.Errorf("failed to list machine config pools: %w", err)
		ch <- prometheus.NewInvalidMetric(clusterUpgradingDesc, err)
		ch <- prometheus.NewInvalidMetric(poolsUpgradingDesc, err)
	}
	poolsUpdating := healthcheck.MachineConfigPoolsUpdating(mcpl)
	ps := sets.NewString()
	for _, p := range poolsUpdating {
		ps.Insert(p.Name)
	}
	for _, mcp := range mcpl.Items {
		ch <- prometheus.MustNewConstMetric(
			poolsUpgradingDesc,
			prometheus.GaugeValue,
			boolToFloat64(ps.Has(mcp.Name)),
			mcp.Name,
		)
	}

	var cv configv1.ClusterVersion
	if err := m.Get(ctx, client.ObjectKey{Name: m.ManagedUpstreamClusterVersionName}, &cv); err != nil {
		ch <- prometheus.NewInvalidMetric(clusterUpgradingDesc, err)
	} else {
		ch <- prometheus.MustNewConstMetric(
			clusterUpgradingDesc,
			prometheus.GaugeValue,
			boolToFloat64(!clusterversion.IsVersionUpgradeCompleted(cv) || len(poolsUpdating) > 0),
		)
	}

	var jobs managedupgradev1beta1.UpgradeJobList
	if err := m.Client.List(ctx, &jobs); err != nil {
		ch <- prometheus.NewInvalidMetric(jobStates, fmt.Errorf("failed to list upgrade jobs: %w", err))
	}

	for _, job := range jobs.Items {
		ch <- prometheus.MustNewConstMetric(
			jobStates,
			prometheus.GaugeValue,
			1,
			job.Name,
			jobState(job),
		)
	}
}

func boolToFloat64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func jobState(job managedupgradev1beta1.UpgradeJob) string {
	if apimeta.IsStatusConditionTrue(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded) {
		return "succeeded"
	} else if apimeta.IsStatusConditionTrue(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed) {
		return "failed"
	} else if apimeta.IsStatusConditionTrue(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted) {
		return "active"
	}
	return "pending"
}
