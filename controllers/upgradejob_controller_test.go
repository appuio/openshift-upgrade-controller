package controllers

import (
	"context"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
)

func Test_UpgradeJobReconciler_Reconcile_E2E_Upgrade(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "9b588658-9671-429c-a762-34106da5795f",
			DesiredUpdate: &configv1.Update{
				Version: "4.5.12",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d732fee6462de7f04f9432f1bb3925f57554db1d8c8d6f3138eea70e5787c7ae",
			},
		},
		Status: configv1.ClusterVersionStatus{
			AvailableUpdates: []configv1.Release{
				{
					Version: "4.5.13",
					Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d094f1952995b3c5fd8e0b19b128905931e1e8fdb4b6cb377857ab0dfddcff47",
				},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorDegraded,
					Status: configv1.ConditionTrue,
				},
			},
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(10 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(time.Hour)),
			DesiredVersion: configv1.Update{
				Version: "4.5.13",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d094f1952995b3c5fd8e0b19b128905931e1e8fdb4b6cb377857ab0dfddcff47",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout:          metav1.Duration{Duration: 12 * time.Hour},
				PreUpgradeHealthChecks:  managedupgradev1beta1.UpgradeJobHealthCheck{},
				PostUpgradeHealthChecks: managedupgradev1beta1.UpgradeJobHealthCheck{},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	step(t, "Scheduled too early", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.Equal(t, time.Hour, res.RequeueAfter)
	})

	clock.Advance(time.Hour + time.Minute)

	step(t, "Start upgrade", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t, apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted))
	})

	step(t, "Lock cluster version", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
		lock, ok := ucv.Annotations[ClusterVersionLockAnnotation]
		require.True(t, ok, "lock annotation must be set")
		require.Equal(t, upgradeJob.Namespace+"/"+upgradeJob.Name, lock, "lock annotation must contain upgrade job reference")
	})

	step(t, "pre upgrade health check", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionFalse(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPreHealthCheckDone),
			"sets condition to false for time out handling",
		)

		// set operators not degraded
		ucv.Status.Conditions = []configv1.ClusterOperatorStatusCondition{
			{
				Type:   configv1.OperatorDegraded,
				Status: configv1.ConditionFalse,
			},
		}
		require.NoError(t, client.Status().Update(ctx, ucv))

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPreHealthCheckDone),
			"sets condition to true when all healthchecks ok",
		)
	})

	step(t, "start upgrade", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))

		require.Equal(t, upgradeJob.Spec.DesiredVersion.Version, ucv.Spec.DesiredUpdate.Version, "should set desired version")
		require.Equal(t, upgradeJob.Spec.DesiredVersion.Image, ucv.Spec.DesiredUpdate.Image)
	})

	step(t, "wait for upgrade to complete", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t, apimeta.IsStatusConditionFalse(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted), "should set condition to false if still in progress")

		ucv.Status.History = append(ucv.Status.History, configv1.UpdateHistory{
			State:   configv1.CompletedUpdate,
			Version: upgradeJob.Spec.DesiredVersion.Version,
			Image:   upgradeJob.Spec.DesiredVersion.Image,
		})
		// setup for post health check
		ucv.Status.Conditions = []configv1.ClusterOperatorStatusCondition{
			{
				Type:   configv1.OperatorDegraded,
				Status: configv1.ConditionTrue,
			},
		}
		require.NoError(t, client.Status().Update(ctx, ucv))

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t, apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted), "should set condition to true if upgrade completed")
	})

	step(t, "post upgrade health check", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionFalse(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPostHealthCheckDone),
			"sets condition to false for time out handling",
		)

		// set operators not degraded
		ucv.Status.Conditions = []configv1.ClusterOperatorStatusCondition{
			{
				Type:   configv1.OperatorDegraded,
				Status: configv1.ConditionFalse,
			},
		}
		require.NoError(t, client.Status().Update(ctx, ucv))

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPostHealthCheckDone),
			"sets condition to true when all healthchecks ok",
		)
	})

	step(t, "finish and cleanup", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded),
			"job should be marked as succeeded",
		)

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
		require.Empty(t, ucv.Annotations[ClusterVersionLockAnnotation], "should clear lock annotation")
	})
}

func Test_UpgradeJobReconciler_Reconcile_Expired(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(-time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-7 * time.Hour)),
		},
	}

	client := controllerClient(t, upgradeJob)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
	require.NoError(t, err)
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	failedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	require.NotNil(t, failedCond, "should set failed condition")
	require.Equal(t, managedupgradev1beta1.UpgradeJobReasonExpired, failedCond.Reason)
}

func Test_UpgradeJobReconciler_Reconcile_UpgradeWithdrawn(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "9b588658-9671-429c-a762-34106da5795f",
			DesiredUpdate: &configv1.Update{
				Version: "4.5.12",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d732fee6462de7f04f9432f1bb3925f57554db1d8c8d6f3138eea70e5787c7ae",
			},
		},
		Status: configv1.ClusterVersionStatus{},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Hour)),
			DesiredVersion: configv1.Update{
				Version: "4.5.13",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:d094f1952995b3c5fd8e0b19b128905931e1e8fdb4b6cb377857ab0dfddcff47",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
				PreUpgradeHealthChecks: managedupgradev1beta1.UpgradeJobHealthCheck{
					SkipDegradedOperatorsCheck: true,
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	for i := 0; i < 10; i++ {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
	}

	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	failedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	require.NotNil(t, failedCond, "should set failed condition")
	require.Equal(t, managedupgradev1beta1.UpgradeJobReasonUpgradeWithdrawn, failedCond.Reason)
	require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
	require.Empty(t, ucv.Annotations[ClusterVersionLockAnnotation], "should clear lock annotation")
}

func Test_UpgradeJobReconciler_Reconcile_Timeout(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Status: configv1.ClusterVersionStatus{
			AvailableUpdates: []configv1.Release{{
				Version: "4.5.13",
			}},
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(3 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Hour)),
			DesiredVersion: configv1.Update{
				Version: "4.5.13",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: time.Hour},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	clock.Advance(2 * time.Hour)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)

	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	failedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	require.NotNil(t, failedCond, "should set failed condition")
	require.Equal(t, managedupgradev1beta1.UpgradeJobReasonTimedOut, failedCond.Reason)
	require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
	require.Empty(t, ucv.Annotations[ClusterVersionLockAnnotation], "should clear lock annotation")
}

func Test_UpgradeJobReconciler_Reconcile_PreHealthCheckTimeout(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorDegraded,
					Status: configv1.ConditionTrue,
				},
			},
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(3 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Hour)),
			DesiredVersion: configv1.Update{
				Version: "4.5.13",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
				PreUpgradeHealthChecks: managedupgradev1beta1.UpgradeJobHealthCheck{
					Timeout: metav1.Duration{Duration: time.Hour},
				},
				PostUpgradeHealthChecks: managedupgradev1beta1.UpgradeJobHealthCheck{
					Timeout: metav1.Duration{Duration: time.Hour},
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	clock.Advance(2 * time.Hour)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)

	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	failedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	require.NotNil(t, failedCond, "should set failed condition")
	require.Equal(t, managedupgradev1beta1.UpgradeJobReasonPreHealthCheckFailed, failedCond.Reason)
	require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
	require.Empty(t, ucv.Annotations[ClusterVersionLockAnnotation], "should clear lock annotation")
}

func Test_UpgradeJobReconciler_Reconcile_PostHealthCheckTimeout(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Status: configv1.ClusterVersionStatus{
			AvailableUpdates: []configv1.Release{
				{Version: "4.5.13"},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorDegraded,
					Status: configv1.ConditionTrue,
				},
			},
			History: []configv1.UpdateHistory{
				{
					Version: "4.5.13",
					State:   configv1.CompletedUpdate,
				},
			},
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(3 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Hour)),
			DesiredVersion: configv1.Update{
				Version: "4.5.13",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
				PreUpgradeHealthChecks: managedupgradev1beta1.UpgradeJobHealthCheck{
					SkipDegradedOperatorsCheck: true,
				},
				PostUpgradeHealthChecks: managedupgradev1beta1.UpgradeJobHealthCheck{
					Timeout: metav1.Duration{Duration: time.Hour},
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 10)
	clock.Advance(2 * time.Hour)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)

	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	failedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	require.NotNil(t, failedCond, "should set failed condition")
	require.Equal(t, managedupgradev1beta1.UpgradeJobReasonPostHealthCheckFailed, failedCond.Reason)
	require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
	require.Empty(t, ucv.Annotations[ClusterVersionLockAnnotation], "should clear lock annotation")
}

func Test_MapClusterVersionEventToUpgradeJob(t *testing.T) {
	testCases := []struct {
		name             string
		eventObj         client.Object
		expectedRequests []reconcile.Request
	}{
		{
			name: "should map locked cluster version to upgrade job",
			eventObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ClusterVersionLockAnnotation: "openshift-upgrade-controller/upgrade-21234-4-5-34",
					},
				},
			},
			expectedRequests: []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Name:      "upgrade-21234-4-5-34",
					Namespace: "openshift-upgrade-controller",
				},
			}},
		}, {
			name: "wrong object",
			eventObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ClusterVersionLockAnnotation: "openshift-upgrade-controller",
					},
				},
			},
			expectedRequests: []reconcile.Request{},
		}, {
			name:             "nil annotations",
			eventObj:         &configv1.ClusterVersion{},
			expectedRequests: []reconcile.Request{},
		}, {
			name: "invalid annotation",
			eventObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ClusterVersionLockAnnotation: "asd",
					},
				},
			},
			expectedRequests: []reconcile.Request{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requests := MapClusterVersionEventToUpgradeJob(tc.eventObj)
			require.Equal(t, tc.expectedRequests, requests)
		})
	}
}

type mockClock struct {
	now time.Time
}

func (m mockClock) Now() time.Time {
	return m.now
}

func (m *mockClock) Advance(d time.Duration) {
	m.now = m.now.Add(d)
}

func requestForObject(o client.Object) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      o.GetName(),
			Namespace: o.GetNamespace(),
		},
	}
}

func step(t *testing.T, msg string, test func(t *testing.T)) {
	t.Logf("STEP: %s", msg)
	test(t)
}

func controllerClient(t *testing.T, initObjs ...client.Object) client.WithWatch {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, configv1.AddToScheme(scheme))
	require.NoError(t, managedupgradev1beta1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initObjs...).
		Build()
}
