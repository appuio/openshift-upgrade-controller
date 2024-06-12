package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
	"github.com/appuio/openshift-upgrade-controller/pkg/healthcheck"
)

//+kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch;update;patch

var clusterUpgradingDesc = prometheus.NewDesc(
	MetricsNamespace+"_cluster_upgrading",
	"Set to 1 if the cluster is currently upgrading, 0 otherwise.",
	[]string{},
	nil,
)

var poolsUpgradingDesc = prometheus.NewDesc(
	MetricsNamespace+"_machine_config_pools_upgrading",
	"Set to 1 if a machine config pool in the cluster is currently upgrading, 0 otherwise. Paused pools are not considered upgrading.",
	[]string{"pool"},
	nil,
)

var poolsPausedDesc = prometheus.NewDesc(
	MetricsNamespace+"_machine_config_pools_paused",
	"Set to 1 if a machine config pool in the cluster is currently paused, 0 otherwise.",
	[]string{"pool"},
	nil,
)

var jobStates = prometheus.NewDesc(
	MetricsNamespace+"_upgradejob_state",
	"Returns the state of jobs in the cluster. 'pending', 'active', 'succeeded', or 'failed' are possible states.",
	[]string{
		"upgradejob",
		"start_after",
		"start_before",
		"desired_version_force",
		"desired_version_image",
		"desired_version_version",
		"state",
		"matches_disruptive_hooks",
	},
	nil,
)

// UpgradeInformationCollector is a Prometheus collector that exposes various metrics about the upgrade process.
type UpgradeInformationCollector struct {
	client.Client

	ManagedUpstreamClusterVersionName string
}

var _ prometheus.Collector = &UpgradeInformationCollector{}

// Describe implements prometheus.Collector.
// Sends the static description of the metrics to the provided channel.
func (*UpgradeInformationCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- clusterUpgradingDesc
	ch <- poolsUpgradingDesc
	ch <- poolsPausedDesc
	ch <- jobStates
}

// Collect implements prometheus.Collector.
// Sends a metric if the cluster is currently upgrading and an upgrading metric for each machine config pool.
// It also collects job states and whether they have matching disruptive hooks.
func (m *UpgradeInformationCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()

	mcpl := machineconfigurationv1.MachineConfigPoolList{}
	if err := m.Client.List(ctx, &mcpl); err != nil {
		err := fmt.Errorf("failed to list machine config pools: %w", err)
		ch <- prometheus.NewInvalidMetric(clusterUpgradingDesc, err)
		ch <- prometheus.NewInvalidMetric(poolsUpgradingDesc, err)
		ch <- prometheus.NewInvalidMetric(poolsPausedDesc, err)
	}
	poolsUpdating := healthcheck.MachineConfigPoolsUpdating(mcpl)
	pus := sets.NewString()
	for _, p := range poolsUpdating {
		if p.Paused {
			continue
		}
		pus.Insert(p.Name)
	}
	for _, mcp := range mcpl.Items {
		ch <- prometheus.MustNewConstMetric(
			poolsUpgradingDesc,
			prometheus.GaugeValue,
			boolToFloat64(pus.Has(mcp.Name)),
			mcp.Name,
		)
		ch <- prometheus.MustNewConstMetric(
			poolsPausedDesc,
			prometheus.GaugeValue,
			boolToFloat64(mcp.Spec.Paused),
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
			boolToFloat64(!clusterversion.IsVersionUpgradeCompleted(cv) || pus.Len() > 0),
		)
	}

	var jobs managedupgradev1beta1.UpgradeJobList
	if err := m.Client.List(ctx, &jobs); err != nil {
		ch <- prometheus.NewInvalidMetric(jobStates, fmt.Errorf("failed to list upgrade jobs: %w", err))
		return
	}

	var jobsHooks managedupgradev1beta1.UpgradeJobHookList
	if err := m.Client.List(ctx, &jobsHooks); err != nil {
		ch <- prometheus.NewInvalidMetric(jobStates, fmt.Errorf("failed to list upgrade job hooks: %w", err))
		return
	}

	for _, job := range jobs.Items {
		v := job.Spec.DesiredVersion
		if v == nil {
			v = &configv1.Update{}
		}
		ch <- prometheus.MustNewConstMetric(
			jobStates,
			prometheus.GaugeValue,
			1,
			job.Name,
			job.Spec.StartAfter.UTC().Format(time.RFC3339),
			job.Spec.StartBefore.UTC().Format(time.RFC3339),
			strconv.FormatBool(v.Force),
			v.Image,
			v.Version,
			jobState(job),
			strconv.FormatBool(jobHasMatchingDisruptiveHook(job, jobsHooks)),
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
	} else if apimeta.IsStatusConditionTrue(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPaused) {
		return "paused"
	} else if apimeta.IsStatusConditionTrue(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted) {
		return "active"
	}
	return "pending"
}

func jobHasMatchingDisruptiveHook(job managedupgradev1beta1.UpgradeJob, hooks managedupgradev1beta1.UpgradeJobHookList) bool {
	for _, hook := range hooks.Items {
		sel, err := metav1.LabelSelectorAsSelector(&hook.Spec.Selector)
		if err != nil {
			log.Log.Error(err, "failed to parse hook selector")
			continue
		}
		if !sel.Matches(labels.Set(job.Labels)) {
			continue
		}
		if hook.WouldExecute(&job) && hook.Spec.Disruptive {
			return true
		}
	}

	return false
}
