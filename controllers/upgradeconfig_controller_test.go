package controllers

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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

func Test_UpgradeConfigReconciler_Reconcile_SuspendedByWindow(t *testing.T) {
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

	recorder := record.NewFakeRecorder(5)
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

func reconcileNTimes(t *testing.T, subject reconcile.Reconciler, ctx context.Context, req reconcile.Request, n int) (lastResult reconcile.Result) {
	t.Helper()
	for i := 0; i < n; i++ {
		lr, err := subject.Reconcile(ctx, req)
		require.NoError(t, err)
		lastResult = lr
	}
	return
}
