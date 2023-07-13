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
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_ClusterUpgradingMetric(t *testing.T) {
	expectedMetricNames := []string{
		"openshift_upgrade_controller_upgradejob_state",
		"openshift_upgrade_controller_cluster_upgrading",
		"openshift_upgrade_controller_machine_config_pools_upgrading",
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

	pendingJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pending",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter:  metav1.Date(2020, 1, 20, 22, 0, 0, 0, time.FixedZone("Test", 60*60*2)),
			StartBefore: metav1.Date(2020, 1, 20, 23, 0, 0, 0, time.FixedZone("Test", 60*60*2)),
			DesiredVersion: configv1.Update{
				Version: "4.11.23",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:26f6d10b18",
				Force:   true,
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
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	c := controllerClient(t, version, masterPool, workerPool, pendingJob, activeJob, succeededJob, failedJob)
	subject := &ClusterUpgradingMetric{
		Client: c,

		ManagedUpstreamClusterVersionName: "version",
	}

	require.NoError(t,
		testutil.CollectAndCompare(subject, expectedMetrics(true, false, false), expectedMetricNames...),
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
		testutil.CollectAndCompare(subject, expectedMetrics(true, false, true), expectedMetricNames...),
		"upgrading should be true if cluster version is progressing or a machine config pool is not fully upgraded",
	)

	workerPool.Status.UpdatedMachineCount = workerPool.Status.MachineCount
	require.NoError(t, c.Status().Update(context.Background(), workerPool))

	require.NoError(t,
		testutil.CollectAndCompare(subject, expectedMetrics(false, false, false), expectedMetricNames...),
		"upgrading should be false if cluster version is not progressing and all machine config pools are fully upgraded",
	)
}

func expectedMetrics(upgrading, masterUpgrading, workerUpgrading bool) io.Reader {
	metrics := `
# HELP openshift_upgrade_controller_cluster_upgrading Set to 1 if the cluster is currently upgrading, 0 otherwise.
# TYPE openshift_upgrade_controller_cluster_upgrading gauge
openshift_upgrade_controller_cluster_upgrading %d
# HELP openshift_upgrade_controller_machine_config_pools_upgrading Set to 1 if a machine config pool in the cluster is currently upgrading, 0 otherwise.
# TYPE openshift_upgrade_controller_machine_config_pools_upgrading gauge
openshift_upgrade_controller_machine_config_pools_upgrading{pool="master"} %d
openshift_upgrade_controller_machine_config_pools_upgrading{pool="worker"} %d
# HELP openshift_upgrade_controller_upgradejob_state Returns the state of jobs in the cluster. 'pending', 'active', 'succeeded', or 'failed' are possible states.
# TYPE openshift_upgrade_controller_upgradejob_state gauge
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="active",upgradejob="active"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="failed",upgradejob="failed"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="false",desired_version_image="",desired_version_version="",start_after="0001-01-01T00:00:00Z",start_before="0001-01-01T00:00:00Z",state="succeeded",upgradejob="succeeded"} 1
openshift_upgrade_controller_upgradejob_state{desired_version_force="true",desired_version_image="quay.io/openshift-release-dev/ocp-release@sha256:26f6d10b18",desired_version_version="4.11.23",start_after="2020-01-20T20:00:00Z",start_before="2020-01-20T21:00:00Z",state="pending",upgradejob="pending"} 1
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
