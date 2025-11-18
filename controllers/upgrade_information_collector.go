package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/api/machineconfiguration/v1"
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
//+kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch

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

var jobStateDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradejob_state",
	"Returns the state of jobs in the cluster. 'pending', 'active', 'succeeded', or 'failed' are possible states. Final states may have a reason.",
	[]string{
		"upgradejob",
		"start_after",
		"start_before",
		"desired_version_force",
		"desired_version_image",
		"desired_version_version",
		"state",
		"reason",
		"matches_disruptive_hooks",
	},
	nil,
)

var jobStartAfterDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradejob_start_after_timestamp_seconds",
	"The value of the startAfter field of the job.",
	[]string{
		"upgradejob",
	},
	nil,
)

var jobStartBeforeDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradejob_start_before_timestamp_seconds",
	"The value of the startBefore field of the job.",
	[]string{
		"upgradejob",
	},
	nil,
)

var upgradeSuspensionWindowInfoDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradesuspensionwindow_info",
	"Information about the upgradesuspensionwindow object",
	[]string{
		"upgradesuspensionwindow",
		"reason",
	},
	nil,
)

var upgradeSuspensionWindowStartDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradesuspensionwindow_start_timestamp_seconds",
	"The value of the start field of the suspension window.",
	[]string{
		"upgradesuspensionwindow",
	},
	nil,
)

var upgradeSuspensionWindowEndDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradesuspensionwindow_end_timestamp_seconds",
	"The value of the start field of the suspension window.",
	[]string{
		"upgradesuspensionwindow",
	},
	nil,
)

var upgradeSuspensionWindowMatchingConfigsDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradesuspensionwindow_matching_config",
	"Matching UpgradeConfigs for the suspension window",
	[]string{
		"upgradesuspensionwindow",
		"config",
	},
	nil,
)

var upgradeConfigInfoDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradeconfig_info",
	"Information about the upgradeconfig object",
	[]string{
		"upgradeconfig",
		"cron",
		"location",
		"suspended",
	},
	nil,
)

var upgradeConfigPinVersionWindowDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradeconfig_pin_version_window_seconds",
	"The value of the pinVersionWindow field of the upgradeconfig.",
	[]string{
		"upgradeconfig",
	},
	nil,
)

var upgradeConfigNextPossibleScheduleDesc = prometheus.NewDesc(
	MetricsNamespace+"_upgradeconfig_next_possible_schedule_timestamp_seconds",
	"The value of the time field of the next possible schedule for an upgrade.",
	[]string{
		"upgradeconfig",
		"n",
		"timestamp",
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
	ch <- jobStateDesc
	ch <- jobStartAfterDesc
	ch <- jobStartBeforeDesc
	ch <- upgradeConfigInfoDesc
	ch <- upgradeConfigPinVersionWindowDesc
	ch <- upgradeConfigNextPossibleScheduleDesc
	ch <- upgradeSuspensionWindowInfoDesc
	ch <- upgradeSuspensionWindowStartDesc
	ch <- upgradeSuspensionWindowEndDesc
	ch <- upgradeSuspensionWindowMatchingConfigsDesc
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

	var windows managedupgradev1beta1.UpgradeSuspensionWindowList
	if err := m.Client.List(ctx, &windows); err != nil {
		ferr := fmt.Errorf("failed to list upgrade suspension windows: %w", err)
		ch <- prometheus.NewInvalidMetric(upgradeSuspensionWindowInfoDesc, ferr)
		ch <- prometheus.NewInvalidMetric(upgradeSuspensionWindowStartDesc, ferr)
		ch <- prometheus.NewInvalidMetric(upgradeSuspensionWindowEndDesc, ferr)
		ch <- prometheus.NewInvalidMetric(upgradeSuspensionWindowMatchingConfigsDesc, ferr)
	} else {
		for _, window := range windows.Items {
			ch <- prometheus.MustNewConstMetric(
				upgradeSuspensionWindowInfoDesc,
				prometheus.GaugeValue,
				1,
				window.Name,
				window.Spec.Reason,
			)
			ch <- prometheus.MustNewConstMetric(
				upgradeSuspensionWindowStartDesc,
				prometheus.GaugeValue,
				float64(window.Spec.Start.Time.Unix()),
				window.Name,
			)
			ch <- prometheus.MustNewConstMetric(
				upgradeSuspensionWindowEndDesc,
				prometheus.GaugeValue,
				float64(window.Spec.End.Time.Unix()),
				window.Name,
			)
			for _, config := range window.Status.MatchingConfigs {
				ch <- prometheus.MustNewConstMetric(
					upgradeSuspensionWindowMatchingConfigsDesc,
					prometheus.GaugeValue,
					1,
					window.Name,
					config.Name,
				)
			}
		}
	}

	var configs managedupgradev1beta1.UpgradeConfigList
	if err := m.Client.List(ctx, &configs); err != nil {
		ferr := fmt.Errorf("failed to list upgrade configs: %w", err)
		ch <- prometheus.NewInvalidMetric(upgradeConfigInfoDesc, ferr)
		ch <- prometheus.NewInvalidMetric(upgradeConfigPinVersionWindowDesc, ferr)
		ch <- prometheus.NewInvalidMetric(upgradeConfigNextPossibleScheduleDesc, ferr)
	} else {
		for _, config := range configs.Items {
			ch <- prometheus.MustNewConstMetric(
				upgradeConfigInfoDesc,
				prometheus.GaugeValue,
				1,
				config.Name,
				config.Spec.Schedule.Cron,
				config.Spec.Schedule.Location,
				strconv.FormatBool(config.Spec.Schedule.Suspend),
			)
			ch <- prometheus.MustNewConstMetric(
				upgradeConfigPinVersionWindowDesc,
				prometheus.GaugeValue,
				float64(config.Spec.PinVersionWindow.Seconds()),
				config.Name,
			)
			for i, nps := range config.Status.NextPossibleSchedules {
				ch <- prometheus.MustNewConstMetric(
					upgradeConfigNextPossibleScheduleDesc,
					prometheus.GaugeValue,
					float64(nps.Time.Unix()),
					config.Name,
					strconv.Itoa(i),
					nps.Time.UTC().Format(time.RFC3339),
				)
			}
		}
	}

	var jobs managedupgradev1beta1.UpgradeJobList
	if err := m.Client.List(ctx, &jobs); err != nil {
		ferr := fmt.Errorf("failed to list upgrade jobs: %w", err)
		ch <- prometheus.NewInvalidMetric(jobStateDesc, ferr)
		ch <- prometheus.NewInvalidMetric(jobStartAfterDesc, ferr)
		ch <- prometheus.NewInvalidMetric(jobStartBeforeDesc, ferr)
		return
	}

	var jobsHooks managedupgradev1beta1.UpgradeJobHookList
	if err := m.Client.List(ctx, &jobsHooks); err != nil {
		ferr := fmt.Errorf("failed to list upgrade job hooks: %w", err)
		ch <- prometheus.NewInvalidMetric(jobStateDesc, ferr)
		ch <- prometheus.NewInvalidMetric(jobStartAfterDesc, ferr)
		ch <- prometheus.NewInvalidMetric(jobStartBeforeDesc, ferr)
		return
	}

	for _, job := range jobs.Items {
		v := job.Spec.DesiredVersion
		if v == nil {
			v = &configv1.Update{}
		}
		ch <- prometheus.MustNewConstMetric(
			jobStateDesc,
			prometheus.GaugeValue,
			1,
			job.Name,
			job.Spec.StartAfter.UTC().Format(time.RFC3339),
			job.Spec.StartBefore.UTC().Format(time.RFC3339),
			strconv.FormatBool(v.Force),
			v.Image,
			v.Version,
			jobState(job),
			jobStateReason(job),
			strconv.FormatBool(jobHasMatchingDisruptiveHook(job, jobsHooks)),
		)
		ch <- prometheus.MustNewConstMetric(
			jobStartAfterDesc,
			prometheus.GaugeValue,
			float64(job.Spec.StartAfter.Unix()),
			job.Name,
		)
		ch <- prometheus.MustNewConstMetric(
			jobStartBeforeDesc,
			prometheus.GaugeValue,
			float64(job.Spec.StartBefore.Unix()),
			job.Name,
		)
	}
}

func boolToFloat64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// jobStateReason returns the reason for the current state of the job.
// All final states should have a reason.
func jobStateReason(job managedupgradev1beta1.UpgradeJob) string {
	sc := apimeta.FindStatusCondition(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded)
	if sc != nil && sc.Status == metav1.ConditionTrue {
		return sc.Reason
	}
	sf := apimeta.FindStatusCondition(job.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	if sf != nil && sf.Status == metav1.ConditionTrue {
		return sf.Reason
	}
	return ""
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
