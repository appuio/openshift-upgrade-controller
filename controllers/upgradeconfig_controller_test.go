package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_UpgradeConfigReconciler_Reconcile_E2E(t *testing.T) {
	ctx := log.IntoContext(t.Context(), testr.New(t))
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
			JobTemplate: managedupgradev1beta1.UpgradeConfigJobTemplate{
				Metadata: metav1.ObjectMeta{
					Labels: map[string]string{"app": "openshift-upgrade-controller"},
				},
			},
		},
	}

	expiredSuspensionWindow := &managedupgradev1beta1.UpgradeSuspensionWindow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "expired-window",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeSuspensionWindowSpec{
			Start: metav1.NewTime(clock.Now().Add(-7 * 24 * time.Hour)),
			End:   metav1.NewTime(clock.Now().Add(-24 * time.Hour)),
			JobSelector: &metav1.LabelSelector{
				MatchLabels: upgradeConfig.Labels,
			},
		},
	}
	unrelatedSuspensionWindow := &managedupgradev1beta1.UpgradeSuspensionWindow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unrelated-window",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeSuspensionWindowSpec{
			Start:       metav1.NewTime(clock.Now().Add(-24 * time.Hour)),
			End:         metav1.NewTime(clock.Now().Add(90 * 24 * time.Hour)),
			JobSelector: &metav1.LabelSelector{},
		},
	}

	client := controllerClient(t, ucv, upgradeConfig, expiredSuspensionWindow, unrelatedSuspensionWindow)

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
		require.Len(t, jobs, 1, "job with empty desired version should be created")
		emptyJob := jobs[0]
		require.Nil(t, emptyJob.Spec.DesiredVersion)
		require.NoError(t, client.Delete(ctx, &emptyJob))
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
		require.NoError(t, client.Status().Update(ctx, ucv))
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
		require.Equal(t, upgradeConfig.Spec.JobTemplate.Metadata.Annotations, job.Annotations)
		require.Equal(t, upgradeConfig.Spec.JobTemplate.Metadata.Labels, job.Labels)
		require.Equal(t, configv1.Update{
			Version: ucv.Status.AvailableUpdates[0].Version,
			Image:   ucv.Status.AvailableUpdates[0].Image,
		}, *job.Spec.DesiredVersion)
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

func Test_UpgradeConfigReconciler_Reconcile_AddNextWindowsToStatus(t *testing.T) {
	ctx := log.IntoContext(t.Context(), testr.New(t))
	clock := mockClock{now: time.Date(2022, time.April, 4, 8, 0, 0, 0, time.UTC)}
	t.Log("Now: ", clock.Now())
	require.Equal(t, 14, func() int { _, isoweek := clock.Now().ISOWeek(); return isoweek }())
	require.Equal(t, time.Monday, clock.Now().Weekday())

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}

	upgradeConfig := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "daily-maintenance",
			Namespace:         "appuio-openshift-upgrade-controller",
			CreationTimestamp: metav1.Time{Time: clock.Now().Add(-time.Hour)},
		},
		Spec: managedupgradev1beta1.UpgradeConfigSpec{
			Schedule: managedupgradev1beta1.UpgradeConfigSchedule{
				Cron:     "0 22 * * *", // At 22:00 every day
				Location: "UTC",
				Suspend:  true,
			},
			MaxSchedulingDelay: metav1.Duration{Duration: time.Minute},
			JobTemplate: managedupgradev1beta1.UpgradeConfigJobTemplate{
				Metadata: metav1.ObjectMeta{
					Labels: map[string]string{"app": "openshift-upgrade-controller"},
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeConfig)

	subject := &UpgradeConfigReconciler{
		Client:   client,
		Scheme:   client.Scheme(),
		Recorder: newFakeRecorder(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	res, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
	require.NoError(t, err)

	var uuc managedupgradev1beta1.UpgradeConfig
	require.NoError(t, client.Get(ctx, types.NamespacedName{Name: upgradeConfig.Name, Namespace: upgradeConfig.Namespace}, &uuc))
	nextTime := time.Date(2022, time.April, 4, 22, 0, 0, 0, time.UTC)
	assert.Equal(t, res.RequeueAfter, nextTime.Sub(clock.Now()))

	expected := make([]string, 10)
	for i := 0; i < 10; i++ {
		expected[i] = nextTime.Format(time.RFC3339)
		nextTime = nextTime.Add(24 * time.Hour)
	}

	got := make([]string, 10)
	for i, t := range uuc.Status.NextPossibleSchedules {
		got[i] = t.Time.UTC().Format(time.RFC3339)
	}

	assert.Equal(t, expected, got)
}

func Test_UpgradeConfigReconciler_Reconcile_SuspendedByWindow(t *testing.T) {
	ctx := log.IntoContext(t.Context(), testr.New(t))
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
		Status: configv1.ClusterVersionStatus{
			AvailableUpdates: []configv1.Release{
				{
					Version: "4.5.13",
					Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d094f1952995b3c5fd8e0b19b128905931e1e8fdb4b6cb377857ab0dfddcff47",
				},
			},
		},
	}

	upgradeConfig := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "daily-maintenance",
			Namespace:         "appuio-openshift-upgrade-controller",
			CreationTimestamp: metav1.Time{Time: clock.Now().Add(-time.Hour)},
		},
		Spec: managedupgradev1beta1.UpgradeConfigSpec{
			Schedule: managedupgradev1beta1.UpgradeConfigSchedule{
				Cron:     "0 22 * * *", // At 22:00 every day
				Location: "UTC",
			},
			PinVersionWindow:     metav1.Duration{Duration: time.Hour},
			MaxSchedulingDelay:   metav1.Duration{Duration: time.Minute},
			MaxUpgradeStartDelay: metav1.Duration{Duration: time.Hour},
			JobTemplate: managedupgradev1beta1.UpgradeConfigJobTemplate{
				Metadata: metav1.ObjectMeta{
					Labels: map[string]string{"app": "openshift-upgrade-controller"},
				},
			},
		},
	}

	suspensionWindow := &managedupgradev1beta1.UpgradeSuspensionWindow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "suspension-window",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeSuspensionWindowSpec{
			Start:          metav1.NewTime(clock.Now().Add(-time.Hour)),
			End:            metav1.NewTime(clock.Now().Add(24 * time.Hour)),
			ConfigSelector: &metav1.LabelSelector{},
		},
	}

	client := controllerClient(t, ucv, upgradeConfig, suspensionWindow)

	recorder := newFakeRecorder()
	subject := &UpgradeConfigReconciler{
		Client:   client,
		Scheme:   client.Scheme(),
		Recorder: recorder,

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	step(t, "not in job creation window", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)
		// 14 hours (08:00->22:00) - 1 hour (version pin window)
		require.Equal(t, (14*time.Hour)-upgradeConfig.Spec.PinVersionWindow.Duration, res.RequeueAfter)
		clock.Advance(res.RequeueAfter)
	})

	step(t, "suspended by window", func(t *testing.T) {
		reconcileNTimes(t, subject, ctx, requestForObject(upgradeConfig), 3)
		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 0)
		requireEventMatches(t, recorder, EventReasonUpgradeConfigSuspendedBySuspensionWindow, suspensionWindow.Name)
	})

	step(t, "window expired, create job", func(t *testing.T) {
		clock.Advance(24 * time.Hour)

		_, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
		require.NoError(t, err)

		jobs := listJobs(t, client, upgradeConfig.Namespace)
		require.Len(t, jobs, 1)
	})
}

// requireEventMatches asserts that an event with the given substrings is present in the recorder.
// It consumes all events from the recorder.
func requireEventMatches(t *testing.T, recorder *record.FakeRecorder, substrings ...string) {
	t.Helper()

	events := make([]string, 0, len(recorder.Events))
	for end := false; !end; {
		select {
		case event := <-recorder.Events:
			events = append(events, event)
		default:
			end = true
		}
	}

	var matchingEvent string
	for _, event := range events {
		matches := true
		for _, substr := range substrings {
			if !strings.Contains(event, substr) {
				matches = false
				break
			}
		}
		if matches {
			matchingEvent = event
			break
		}
	}

	require.NotEmpty(t, matchingEvent, "no event matches %v, got %v", substrings, events)
}

func Test_UpgradeConfigReconciler_Reconcile_CleanupSuccessfulJobs(t *testing.T) {
	ctx := log.IntoContext(t.Context(), testr.New(t))
	clock := mockClock{now: time.Date(2022, time.April, 4, 8, 0, 0, 0, time.UTC)}

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}

	upgradeConfig := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "daily-maintenance",
			Namespace:         "appuio-openshift-upgrade-controller",
			CreationTimestamp: metav1.Time{Time: clock.Now().Add(-time.Hour)},
		},
		Spec: managedupgradev1beta1.UpgradeConfigSpec{
			Schedule: managedupgradev1beta1.UpgradeConfigSchedule{
				Cron: "0 22 * * *", // At 22:00 every day
			},
			SuccessfulJobsHistoryLimit: ptr.To(2),
		},
	}

	client := controllerClient(t, ucv, upgradeConfig)

	jobStartTs := []time.Time{
		clock.Now().Add(-24 * time.Hour * 1),
		clock.Now().Add(-24 * time.Hour * 2),
		clock.Now().Add(-24 * time.Hour * 3),
	}

	for _, ts := range jobStartTs {
		job := &managedupgradev1beta1.UpgradeJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("job-%d", ts.Unix()),
				Namespace: upgradeConfig.Namespace,
			},
			Spec: managedupgradev1beta1.UpgradeJobSpec{
				StartAfter: metav1.NewTime(ts),
			},
		}
		{
			successfulOwned := job.DeepCopy()
			successfulOwned.Name = "successful-owned-" + successfulOwned.Name
			require.NoError(t, controllerutil.SetControllerReference(upgradeConfig, successfulOwned, client.Scheme()))
			require.NoError(t, client.Create(ctx, successfulOwned))
			apimeta.SetStatusCondition(&successfulOwned.Status.Conditions, metav1.Condition{
				Type:   managedupgradev1beta1.UpgradeJobConditionSucceeded,
				Status: metav1.ConditionTrue,
			})
			require.NoError(t, client.Status().Update(ctx, successfulOwned))
		}
		{
			notSuccessfulOwned := job.DeepCopy()
			notSuccessfulOwned.Name = "not-successful-owned-" + notSuccessfulOwned.Name
			require.NoError(t, controllerutil.SetControllerReference(upgradeConfig, notSuccessfulOwned, client.Scheme()))
			require.NoError(t, client.Create(ctx, notSuccessfulOwned))
		}
		{
			successfulNotOwned := job.DeepCopy()
			successfulNotOwned.Name = "successful-not-owned-" + successfulNotOwned.Name
			require.NoError(t, client.Create(ctx, successfulNotOwned))
			apimeta.SetStatusCondition(&successfulNotOwned.Status.Conditions, metav1.Condition{
				Type:   managedupgradev1beta1.UpgradeJobConditionSucceeded,
				Status: metav1.ConditionTrue,
			})
			require.NoError(t, client.Status().Update(ctx, successfulNotOwned))
		}
	}

	subject := &UpgradeConfigReconciler{
		Client:   client,
		Scheme:   client.Scheme(),
		Recorder: newFakeRecorder(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	_, err := subject.Reconcile(ctx, requestForObject(upgradeConfig))
	require.NoError(t, err)

	var nj managedupgradev1beta1.UpgradeJobList
	require.NoError(t, client.List(ctx, &nj))
	names := make([]string, 0, len(nj.Items))
	for _, j := range nj.Items {
		names = append(names, j.Name)
	}
	require.ElementsMatch(t, names, []string{
		"not-successful-owned-job-1648800000",
		"not-successful-owned-job-1648886400",
		"not-successful-owned-job-1648972800",
		"successful-not-owned-job-1648800000",
		"successful-not-owned-job-1648886400",
		"successful-not-owned-job-1648972800",
		"successful-owned-job-1648886400",
		"successful-owned-job-1648972800",
	}, "only owned and successful jobs should be cleaned up, and only the oldest ones")
}

// requireTimeEqual asserts that two times are equal, ignoring their timezones.
func requireTimeEqual(t *testing.T, expected, actual time.Time, msgAndArgs ...any) {
	t.Helper()
	require.Equal(t, expected.UTC(), actual.UTC(), msgAndArgs...)
}

func listJobs(t *testing.T, c client.Client, namespace string) []managedupgradev1beta1.UpgradeJob {
	t.Helper()
	var jobs managedupgradev1beta1.UpgradeJobList
	require.NoError(t, c.List(t.Context(), &jobs, client.InNamespace(namespace)))
	return jobs.Items
}

func newFakeRecorder() *record.FakeRecorder {
	return record.NewFakeRecorder(100)
}

func reconcileNTimes(t *testing.T, subject reconcile.Reconciler, ctx context.Context, req reconcile.Request, n int) (lastResult reconcile.Result) {
	t.Helper()
	for i := 0; i < n; i++ {
		lr, err := subject.Reconcile(ctx, req)
		require.NoError(t, err)
		lastResult = lr
	}
	return
}
