package controllers

import (
	"context"
	"strconv"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_UpgradeConfigReconciler_Reconcile_E2E(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, time.April, 4, 8, 0, 0, 0, time.UTC)}
	t.Log("Now: ", clock.Now())
	require.Equal(t, 14, func() int { _, isoweek := clock.Now().ISOWeek(); return isoweek }())
	require.Equal(t, time.Monday, clock.Now().Weekday())

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "9b588658-9671-429c-a762-34106da5795f",
		},
	}

	upgradeConfig := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "biweekly-maintenance",
			Namespace:         "appuio-openshift-upgrade-controller",
			CreationTimestamp: metav1.Time{Time: clock.Now().Add(-time.Hour)},
		},
		Spec: managedupgradev1beta1.UpgradeConfigSpec{
			Schedule: managedupgradev1beta1.UpgradeConfigSchedule{
				Cron:     "0 22 * * 2", // At 22:00 on Tuesday
				IsoWeek:  "@odd",
				Location: "UTC",
			},
			PinVersionWindow:     metav1.Duration{Duration: time.Hour},
			MaxSchedulingDelay:   metav1.Duration{Duration: time.Minute},
			MaxUpgradeStartDelay: metav1.Duration{Duration: time.Hour},
		},
	}

	client := controllerClient(t, ucv, upgradeConfig)

	subject := &UpgradeConfigReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	step(t, "not in job creation window", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		// Tuesday in a week + 14 hours (08:00->22:00) - 1 hour (version pin window)
		require.Equal(t, (8*24*time.Hour)+(14*time.Hour)-upgradeConfig.Spec.PinVersionWindow.Duration, res.RequeueAfter)
		clock.Advance(res.RequeueAfter)
	})

	step(t, "no upgrade available", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)

		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 0, "no upgrade available, no job should be scheduled")
		var uuc managedupgradev1beta1.UpgradeConfig
		expectedStartAfter := clock.Now().Add(upgradeConfig.Spec.PinVersionWindow.Duration)
		require.NoError(t, client.Get(ctx, types.NamespacedName{Name: upgradeConfig.Name, Namespace: upgradeConfig.Namespace}, &uuc))
		require.Equal(t, expectedStartAfter.In(time.UTC), uuc.Status.LastScheduledUpgrade.In(time.UTC), "last scheduled time should be updated")

		clock.Advance(24 * time.Hour)
	})

	step(t, "not in job creation window", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		// In two weeks minus 1 day already advanced in previous step
		require.Equal(t, (14*24*time.Hour)-(24*time.Hour), res.RequeueAfter)

		clock.Advance(res.RequeueAfter - 7*time.Hour)
		res, err = subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		require.Equal(t, 7*time.Hour, res.RequeueAfter, "intermediate requeue should not reset the requeue time")
		clock.Advance(res.RequeueAfter)
	})

	step(t, "make update available", func(t *testing.T) {
		ucv.Status.AvailableUpdates = []configv1.Release{
			{
				Version: "4.5.13",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d094f1952995b3c5fd8e0b19b128905931e1e8fdb4b6cb377857ab0dfddcff47",
			},
			{
				Version: "4.5.12",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:4e5ace08e0807f18300d33e51251bb3dea3f9ba3e2dac0f0b5f8ba13581c6193",
			},
		}
		require.NoError(t, client.Update(ctx, ucv))
	})

	step(t, "create upgrade job", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)

		expectedStartAfter := clock.Now().Add(upgradeConfig.Spec.PinVersionWindow.Duration)

		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 1)
		job := jobs[0]

		require.Equal(t, "biweekly-maintenance-4-5-13-"+strconv.FormatInt(expectedStartAfter.Unix(), 10), job.Name)
		requireTimeEqual(t, expectedStartAfter, job.Spec.StartAfter.Time)
		requireTimeEqual(t, expectedStartAfter.Add(upgradeConfig.Spec.MaxUpgradeStartDelay.Duration), job.Spec.StartBefore.Time)
		require.Equal(t, upgradeConfig.Spec.JobTemplate.Spec.Config, job.Spec.UpgradeJobConfig)
		require.Equal(t, configv1.Update{
			Version: ucv.Status.AvailableUpdates[0].Version,
			Image:   ucv.Status.AvailableUpdates[0].Image,
		}, job.Spec.DesiredVersion)
	})

	step(t, "there is a future job. do nothing", func(t *testing.T) {
		clock.Advance(30 * time.Minute)

		_, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 1)
	})

	step(t, "job still running, don't schedule new one", func(t *testing.T) {
		clock.Advance(time.Hour)

		reconcileNTimes(t, subject, ctx, requestForObject(upgradeConfig), 3)

		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 1)
	})

	step(t, "job succeeded, schedule new one", func(t *testing.T) {
		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 1)
		job := jobs[0]

		job.Status.Conditions = []metav1.Condition{
			{
				Type:   managedupgradev1beta1.UpgradeJobConditionSucceeded,
				Status: metav1.ConditionTrue,
			},
		}
		require.NoError(t, client.Status().Update(ctx, &job))

		res, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		require.Greater(t, res.RequeueAfter, time.Duration(0))
		clock.Advance(res.RequeueAfter)
		_, err = subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		jobs = listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 2)
	})
}

// requireTimeEqual asserts that two times are equal, ignoring their timezones.
func requireTimeEqual(t *testing.T, expected, actual time.Time, msgAndArgs ...any) {
	t.Helper()
	require.Equal(t, expected.UTC(), actual.UTC(), msgAndArgs...)
}

func listJobs(t *testing.T, c client.Client, namespace string) []managedupgradev1beta1.UpgradeJob {
	t.Helper()
	var jobs managedupgradev1beta1.UpgradeJobList
	require.NoError(t, c.List(context.Background(), &jobs, client.InNamespace(namespace)))
	return jobs.Items
}

func reconcileNTimes(t *testing.T, subject reconcile.Reconciler, ctx context.Context, req reconcile.Request, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := subject.Reconcile(ctx, req)
		require.NoError(t, err)
	}
}
