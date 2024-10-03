package controllers

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/api/machineconfiguration/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_ClusterUpgradingMetric(t *testing.T) {
	expectedMetricNames := []string{
		"openshift_upgrade_controller_upgradejob_state",
		"openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds",
		"openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds",
		"openshift_upgrade_controller_cluster_upgrading",
		"openshift_upgrade_controller_machine_config_pools_upgrading",
		"openshift_upgrade_controller_machine_config_pools_paused",
	}

	version := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			DesiredUpdate: &configv1.Update{
				Version: "4.11.23",
			},
		},
	}
	masterPool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "master",
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 3,
		},
	}
	workerPool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker",
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 3,
		},
	}
	pausedPool1 := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "paused1",
		},
		Spec: machineconfigurationv1.MachineConfigPoolSpec{
			Paused: true,
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 0,
		},
	}
	pausedPool2 := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "paused2",
		},
		Spec: machineconfigurationv1.MachineConfigPoolSpec{
			Paused: true,
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 3,
		},
	}

	pendingJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pending",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter:  metav1.Date(2020, 1, 20, 22, 0, 0, 0, time.FixedZone("Test", 60*60*2)),
			StartBefore: metav1.Date(2020, 1, 20, 23, 0, 0, 0, time.FixedZone("Test", 60*60*2)),
			DesiredVersion: &configv1.Update{
				Version: "4.11.23",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:26f6d10b18",
				Force:   true,
			},
		},
	}
	disruptiveJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "disruptive",
			Labels: map[string]string{
				"upgrade": "disruptive",
			},
		},
	}
	disruptiveJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name: disruptiveJob.Name,
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: disruptiveJob.Labels,
			},
			Disruptive: true,
		},
	}
	disruptiveUnclaimedNextJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "disruptive-unclaimed-next",
			Labels: map[string]string{
				"upgrade": "disruptive-unclaimed-next",
			},
		},
	}
	disruptiveUnclaimedNextJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name: disruptiveUnclaimedNextJob.Name,
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: disruptiveUnclaimedNextJob.Labels,
			},
			Disruptive: true,
			Run:        managedupgradev1beta1.RunNext,
		},
	}
	disruptiveClaimedNextJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "disruptive-claimed-next",
			Labels: map[string]string{
				"upgrade": "disruptive-claimed-next",
			},
		},
	}
	disruptiveClaimedNextJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name: disruptiveClaimedNextJob.Name,
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: disruptiveClaimedNextJob.Labels,
			},
			Disruptive: true,
			Run:        managedupgradev1beta1.RunNext,
		},
		Status: managedupgradev1beta1.UpgradeJobHookStatus{
			ClaimedBy: managedupgradev1beta1.ClaimReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Name:       "someone else",
			},
		},
	}
	activeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "active",
		},
		Status: managedupgradev1beta1.UpgradeJobStatus{
			Conditions: []metav1.Condition{
				{
					Type:   managedupgradev1beta1.UpgradeJobConditionStarted,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	pausedJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "paused",
		},
		Status: managedupgradev1beta1.UpgradeJobStatus{
			Conditions: []metav1.Condition{
				{
					Type:   managedupgradev1beta1.UpgradeJobConditionStarted,
					Status: metav1.ConditionTrue,
				},
				{
					Type:   managedupgradev1beta1.UpgradeJobConditionPaused,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	succeededJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "succeeded",
		},
		Status: managedupgradev1beta1.UpgradeJobStatus{
			Conditions: []metav1.Condition{
				{
					Type:   managedupgradev1beta1.UpgradeJobConditionStarted,
					Status: metav1.ConditionTrue,
				}, {
					Type:   managedupgradev1beta1.UpgradeJobConditionSucceeded,
					Reason: managedupgradev1beta1.UpgradeJobReasonSkipped,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	failedJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "failed",
		},
		Status: managedupgradev1beta1.UpgradeJobStatus{
			Conditions: []metav1.Condition{
				{
					Type:   managedupgradev1beta1.UpgradeJobConditionStarted,
					Status: metav1.ConditionTrue,
				}, {
					Type:   managedupgradev1beta1.UpgradeJobConditionFailed,
					Reason: managedupgradev1beta1.UpgradeJobReasonHookFailed,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	c := controllerClient(t, version, masterPool, workerPool, pendingJob, activeJob, pausedJob, succeededJob, failedJob,
		pausedPool1, pausedPool2,
		disruptiveJob, disruptiveUnclaimedNextJob, disruptiveClaimedNextJob,
		disruptiveJobHook, disruptiveUnclaimedNextJobHook, disruptiveClaimedNextJobHook,
	)
	subject := &UpgradeInformationCollector{
		Client: c,

		ManagedUpstreamClusterVersionName: "version",
	}

	require.NoError(t,
		testutil.CollectAndCompare(subject, expectedUpgradingMetrics(true, false, false), expectedMetricNames...),
		"upgrading should be true if cluster version is progressing",
	)

	version.Status.History = append(version.Status.History, configv1.UpdateHistory{
		State:       configv1.CompletedUpdate,
		StartedTime: metav1.Now(),
		Version:     version.Spec.DesiredUpdate.Version,
	})
	require.NoError(t, c.Status().Update(context.Background(), version))
	workerPool.Status.UpdatedMachineCount = workerPool.Status.MachineCount - 1
	require.NoError(t, c.Status().Update(context.Background(), workerPool))

	require.NoError(t,
		testutil.CollectAndCompare(subject, expectedUpgradingMetrics(true, false, true), expectedMetricNames...),
		"upgrading should be true if cluster version is progressing or a machine config pool is not fully upgraded",
	)

	workerPool.Status.UpdatedMachineCount = workerPool.Status.MachineCount
	require.NoError(t, c.Status().Update(context.Background(), workerPool))

	require.NoError(t,
		testutil.CollectAndCompare(subject, expectedUpgradingMetrics(false, false, false), expectedMetricNames...),
		"upgrading should be false if cluster version is not progressing and all machine config pools are fully upgraded",
	)
}

func Test_UpgradeSuspensionWindowMetric(t *testing.T) {
	expectedMetricNames := []string{
		"openshift_upgrade_controller_upgradesuspensionwindow_info",
		"openshift_upgrade_controller_upgradesuspensionwindow_start_timestamp_seconds",
		"openshift_upgrade_controller_upgradesuspensionwindow_end_timestamp_seconds",
		"openshift_upgrade_controller_upgradesuspensionwindow_matching_config",
	}

	version := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}
	usw := &managedupgradev1beta1.UpgradeSuspensionWindow{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mywindow",
		},
		Spec: managedupgradev1beta1.UpgradeSuspensionWindowSpec{
			Start:  metav1.NewTime(time.Date(2022, 3, 1, 0, 0, 0, 0, time.UTC)),
			End:    metav1.NewTime(time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)),
			Reason: "No moar upgrades",
		},
		Status: managedupgradev1beta1.UpgradeSuspensionWindowStatus{
			MatchingConfigs: []managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject{
				{Name: "matching1"},
				{Name: "matching2"},
			},
		},
	}
	c := controllerClient(t, version, usw)
	subject := &UpgradeInformationCollector{
		Client: c,

		ManagedUpstreamClusterVersionName: "version",
	}

	metrics := `
# HELP openshift_upgrade_controller_upgradesuspensionwindow_info Information about the upgradesuspensionwindow object
# TYPE openshift_upgrade_controller_upgradesuspensionwindow_info gauge
openshift_upgrade_controller_upgradesuspensionwindow_info{reason="No moar upgrades",upgradesuspensionwindow="mywindow"} 1
# HELP openshift_upgrade_controller_upgradesuspensionwindow_matching_config Matching UpgradeConfigs for the suspension window
# TYPE openshift_upgrade_controller_upgradesuspensionwindow_matching_config gauge
openshift_upgrade_controller_upgradesuspensionwindow_matching_config{config="matching1",upgradesuspensionwindow="mywindow"} 1
openshift_upgrade_controller_upgradesuspensionwindow_matching_config{config="matching2",upgradesuspensionwindow="mywindow"} 1
# HELP openshift_upgrade_controller_upgradesuspensionwindow_start_timestamp_seconds The value of the start field of the suspension window.
# TYPE openshift_upgrade_controller_upgradesuspensionwindow_start_timestamp_seconds gauge
openshift_upgrade_controller_upgradesuspensionwindow_start_timestamp_seconds{upgradesuspensionwindow="mywindow"} 1.6460928e+09
# HELP openshift_upgrade_controller_upgradesuspensionwindow_end_timestamp_seconds The value of the start field of the suspension window.
# TYPE openshift_upgrade_controller_upgradesuspensionwindow_end_timestamp_seconds gauge
openshift_upgrade_controller_upgradesuspensionwindow_end_timestamp_seconds{upgradesuspensionwindow="mywindow"} 1.6540416e+09
`

	require.NoError(t,
		testutil.CollectAndCompare(subject, strings.NewReader(metrics), expectedMetricNames...),
	)
}

func Test_UpgradeConfigMetric(t *testing.T) {
	expectedMetricNames := []string{
		"openshift_upgrade_controller_upgradeconfig_info",
		"openshift_upgrade_controller_upgradeconfig_pin_version_window_seconds",
		"openshift_upgrade_controller_upgradeconfig_next_possible_schedule_timestamp_seconds",
	}

	version := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}
	uc := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myconfig",
		},
		Spec: managedupgradev1beta1.UpgradeConfigSpec{
			Schedule: managedupgradev1beta1.UpgradeConfigSchedule{
				Cron:     "0 22 * * *",
				IsoWeek:  "@odd",
				Location: "UTC",
			},
			PinVersionWindow: metav1.Duration{Duration: time.Hour},
		},
		Status: managedupgradev1beta1.UpgradeConfigStatus{
			NextPossibleSchedules: []managedupgradev1beta1.NextPossibleSchedule{
				{
					Time: metav1.Time{Time: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)},
				}, {
					Time: metav1.Time{Time: time.Date(2022, 12, 24, 22, 45, 0, 0, time.UTC)},
				}, {
					Time: metav1.Time{Time: time.Date(2023, 2, 7, 22, 45, 0, 0, time.UTC)},
				},
			},
		},
	}
	c := controllerClient(t, version, uc)
	subject := &UpgradeInformationCollector{
		Client: c,

		ManagedUpstreamClusterVersionName: "version",
	}

	metrics := `
# HELP openshift_upgrade_controller_upgradeconfig_info Information about the upgradeconfig object
# TYPE openshift_upgrade_controller_upgradeconfig_info gauge
openshift_upgrade_controller_upgradeconfig_info{cron="0 22 * * *",location="UTC",suspended="false",upgradeconfig="myconfig"} 1

# HELP openshift_upgrade_controller_upgradeconfig_pin_version_window_seconds The value of the pinVersionWindow field of the upgradeconfig.
# TYPE openshift_upgrade_controller_upgradeconfig_pin_version_window_seconds gauge
openshift_upgrade_controller_upgradeconfig_pin_version_window_seconds{upgradeconfig="myconfig"} 3600

# HELP openshift_upgrade_controller_upgradeconfig_next_possible_schedule_timestamp_seconds The value of the time field of the next possible schedule for an upgrade.
# TYPE openshift_upgrade_controller_upgradeconfig_next_possible_schedule_timestamp_seconds gauge
openshift_upgrade_controller_upgradeconfig_next_possible_schedule_timestamp_seconds{n="0",timestamp="2022-12-04T22:45:00Z",upgradeconfig="myconfig"} 1.6701939e+09
openshift_upgrade_controller_upgradeconfig_next_possible_schedule_timestamp_seconds{n="1",timestamp="2022-12-24T22:45:00Z",upgradeconfig="myconfig"} 1.6719219e+09
openshift_upgrade_controller_upgradeconfig_next_possible_schedule_timestamp_seconds{n="2",timestamp="2023-02-07T22:45:00Z",upgradeconfig="myconfig"} 1.6758099e+09
`

	require.NoError(t,
		testutil.CollectAndCompare(subject, strings.NewReader(metrics), expectedMetricNames...),
	)
}

func expectedUpgradingMetrics(upgrading, masterUpgrading, workerUpgrading bool) io.Reader {
	metrics := `
# HELP openshift_upgrade_controller_cluster_upgrading Set to 1 if the cluster is currently upgrading, 0 otherwise.
# TYPE openshift_upgrade_controller_cluster_upgrading gauge
openshift_upgrade_controller_cluster_upgrading %d
# HELP openshift_upgrade_controller_machine_config_pools_paused Set to 1 if a machine config pool in the cluster is currently paused, 0 otherwise.
# TYPE openshift_upgrade_controller_machine_config_pools_paused gauge
openshift_upgrade_controller_machine_config_pools_paused{pool="master"} 0
openshift_upgrade_controller_machine_config_pools_paused{pool="paused1"} 1
openshift_upgrade_controller_machine_config_pools_paused{pool="paused2"} 1
openshift_upgrade_controller_machine_config_pools_paused{pool="worker"} 0
# HELP openshift_upgrade_controller_machine_config_pools_upgrading Set to 1 if a machine config pool in the cluster is currently upgrading, 0 otherwise. Paused pools are not considered upgrading.
# TYPE openshift_upgrade_controller_machine_config_pools_upgrading gauge
openshift_upgrade_controller_machine_config_pools_upgrading{pool="master"} %d
openshift_upgrade_controller_machine_config_pools_upgrading{pool="worker"} %d
openshift_upgrade_controller_machine_config_pools_upgrading{pool="paused1"} 0
openshift_upgrade_controller_machine_config_pools_upgrading{pool="paused2"} 0
# HELP openshift_upgrade_controller_upgradejob_state Returns the state of jobs in the cluster. 'pending', 'active', 'succeeded', or 'failed' are possible states. Final states may have a reason.
# TYPE openshift_upgrade_controller_upgradejob_state gauge
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="false",reason="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="active",upgradejob="active"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="false",reason="HookFailed",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="failed",upgradejob="failed"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="false",reason="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="paused",upgradejob="paused"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="false",reason="Skipped",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="succeeded",upgradejob="succeeded"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="true",desired_version_image="quay.io/openshift-release-dev/ocp-release@sha256:26f6d10b18",desired_version_version="4.11.23",matches_disruptive_hooks="false",reason="",start_after="2020-01-20T20:00:00Z",start_before="2020-01-20T21:00:00Z",state="pending",upgradejob="pending"} 1

openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="true",reason="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="pending",upgradejob="disruptive"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="true",reason="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="pending",upgradejob="disruptive-unclaimed-next"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",matches_disruptive_hooks="false",reason="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="pending",upgradejob="disruptive-claimed-next"} 1

# HELP openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds The value of the startAfter field of the job.
# TYPE openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds gauge
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="active"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="disruptive"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="disruptive-claimed-next"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="disruptive-unclaimed-next"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="failed"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="paused"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="pending"} 1.5795504e+09
openshift_upgrade_controller_upgradejob_start_after_timestamp_seconds{upgradejob="succeeded"} -6.21355968e+10
# HELP openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds The value of the startBefore field of the job.
# TYPE openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds gauge
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="active"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="disruptive"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="disruptive-claimed-next"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="disruptive-unclaimed-next"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="failed"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="paused"} -6.21355968e+10
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="pending"} 1.579554e+09
openshift_upgrade_controller_upgradejob_start_before_timestamp_seconds{upgradejob="succeeded"} -6.21355968e+10
`
	return strings.NewReader(
		fmt.Sprintf(metrics, b2i(upgrading), b2i(masterUpgrading), b2i(workerUpgrading)),
	)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
