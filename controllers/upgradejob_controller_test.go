package controllers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
			Labels: map[string]string{
				"name": "upgrade-1234-4-5-13",
			},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(10 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(time.Hour)),
			DesiredVersion: &configv1.Update{
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
	upgradeJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Repeat("notify", 10),
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventCreate,
				managedupgradev1beta1.EventStart,
				managedupgradev1beta1.EventUpgradeComplete,
				managedupgradev1beta1.EventFinish,
				managedupgradev1beta1.EventSuccess,
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

	c := controllerClient(t, ucv, upgradeJob, upgradeJobHook, masterPool)

	subject := &UpgradeJobReconciler{
		Client: c,
		Scheme: c.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	step(t, "`Create` hook", func(t *testing.T) {
		checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventCreate, false)
	})

	step(t, "Scheduled too early", func(t *testing.T) {
		res, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.Equal(t, time.Hour, res.RequeueAfter)
	})

	clock.Advance(time.Hour + time.Minute)

	step(t, "Start upgrade", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t, apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted))
	})

	step(t, "`Start` hook", func(t *testing.T) {
		checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventStart, false)
	})

	step(t, "Lock cluster version", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
		lock, ok := ucv.Annotations[ClusterVersionLockAnnotation]
		require.True(t, ok, "lock annotation must be set")
		require.Equal(t, upgradeJob.Namespace+"/"+upgradeJob.Name, lock, "lock annotation must contain upgrade job reference")
	})

	step(t, "pre upgrade health check", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
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
		require.NoError(t, c.Status().Update(ctx, ucv))

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPreHealthCheckDone),
			"sets condition to true when all healthchecks ok",
		)
	})

	step(t, "start upgrade", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(ucv).NamespacedName, ucv))

		require.Equal(t, upgradeJob.Spec.DesiredVersion.Version, ucv.Spec.DesiredUpdate.Version, "should set desired version")
		require.Equal(t, upgradeJob.Spec.DesiredVersion.Image, ucv.Spec.DesiredUpdate.Image)
	})

	step(t, "mark master pool as updating", func(t *testing.T) {
		masterPool.Status.UpdatedMachineCount = masterPool.Status.MachineCount - 1
		require.NoError(t, c.Status().Update(ctx, masterPool))
	})

	step(t, "wait for upgrade to complete", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
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
		require.NoError(t, c.Status().Update(ctx, ucv))

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t, apimeta.IsStatusConditionFalse(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted), "machine pool still upgrading")

		masterPool.Status.UpdatedMachineCount = masterPool.Status.MachineCount
		require.NoError(t, c.Status().Update(ctx, masterPool))

		reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t, apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted), "should set condition to true if upgrade completed")
	})

	step(t, "post upgrade health check", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
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
		require.NoError(t, c.Status().Update(ctx, ucv))

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPostHealthCheckDone),
			"sets condition to true when all healthchecks ok",
		)
	})

	step(t, "`UpgradeComplete` hook", func(t *testing.T) {
		checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventUpgradeComplete, true)
	})

	step(t, "finish and cleanup", func(t *testing.T) {
		_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
		require.True(t,
			apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded),
			"job should be marked as succeeded",
		)

		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
		require.Empty(t, ucv.Annotations[ClusterVersionLockAnnotation], "should clear lock annotation")
		_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
		require.NoError(t, err, "should ignore requests if cluster version is not locked")
	})

	step(t, "`Success` and `Finish` hooks", func(t *testing.T) {
		checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventSuccess, true)
		checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventFinish, true)

		clock.Advance(24 * time.Hour)

		var jobs batchv1.JobList
		require.NoError(t, c.List(ctx, &jobs))
		prevJobs := len(jobs.Items)
		upgradeJobHook2 := upgradeJobHook.DeepCopy()
		upgradeJobHook2.ObjectMeta = metav1.ObjectMeta{
			Name:              "notify2",
			Namespace:         upgradeJobHook.Namespace,
			CreationTimestamp: metav1.NewTime(clock.Now()),
		}
		require.NoError(t, c.Create(ctx, upgradeJobHook2))
		reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
		require.NoError(t, c.List(ctx, &jobs))
		require.Equal(t, prevJobs, len(jobs.Items), "should have not created new jobs for hooks created after the upgrade job finished")
	})
}

func Test_UpgradeJobReconciler_Reconcile_EmptyDesiredVersion(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-123123",
			Namespace: "appuio-openshift-upgrade-controller",
			Labels:    map[string]string{"test": "test"},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Hour)),
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
			},
		},
	}
	upgradeJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "notify",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			Run: managedupgradev1beta1.RunAll,
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventStart,
				managedupgradev1beta1.EventUpgradeComplete,
				managedupgradev1beta1.EventSuccess,
				managedupgradev1beta1.EventFinish,
			},
		},
	}

	c := controllerClient(t, upgradeJob, upgradeJobHook,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			}})

	subject := &UpgradeJobReconciler{
		Client: c,
		Scheme: c.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventStart, false)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventUpgradeComplete, false)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventSuccess, false)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventFinish, false)
}

func Test_UpgradeJobReconciler_Reconcile_HookFailed(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
			Labels:    map[string]string{"test": "test"},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(-time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-7 * time.Hour)),
		},
	}
	upgradeJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "notify",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			FailurePolicy: managedupgradev1beta1.FailurePolicyAbort,
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventCreate,
				managedupgradev1beta1.EventFailure,
				managedupgradev1beta1.EventFinish,
			},
		},
	}

	c := controllerClient(t, upgradeJob, upgradeJobHook,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			}})

	subject := &UpgradeJobReconciler{
		Client: c,
		Scheme: c.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventCreate, true)

	_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	require.True(t,
		apimeta.IsStatusConditionTrue(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed),
		"hook failure with failure policy Abort should mark job as failed",
	)

	checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventFailure, false)

	require.NoError(t, c.Delete(ctx, upgradeJob))
	// Reconcile can still be called after the job is deleted since we have controller references with finalizers
	_, err = subject.Reconcile(ctx, requestForObject(upgradeJob))
	require.NoError(t, err)
	var jobs batchv1.JobList
	require.NoError(t, c.List(ctx, &jobs))
	finalizers := []string{}
	for _, job := range jobs.Items {
		finalizers = append(finalizers, job.Finalizers...)
	}
	require.Empty(t, finalizers, "finalizers should be empty or job deleted")
}

func Test_UpgradeJobReconciler_Reconcile_HookJobContainerEnv(t *testing.T) {
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
			Labels:    map[string]string{"test": "test"},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(-time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-7 * time.Hour)),
			DesiredVersion: &configv1.Update{
				Version: "4.5.13",
			},
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
	upgradeJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "notify",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			FailurePolicy: managedupgradev1beta1.FailurePolicyAbort,
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventCreate,
			},
			Template: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test1",
									Env: []corev1.EnvVar{{
										Name:  "TEST",
										Value: "test",
									}},
								},
								{
									Name: "test2",
									Env: []corev1.EnvVar{{
										Name:  "TEST",
										Value: "test",
									}},
								},
							},
						},
					},
				},
			},
		},
	}

	c := controllerClient(t, upgradeJob, upgradeJobHook,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			}})

	subject := &UpgradeJobReconciler{
		Client: c,
		Scheme: c.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	job := checkAndCompleteHook(t, c, subject, upgradeJob, upgradeJobHook, managedupgradev1beta1.EventCreate, true)

	require.Equal(t, 2, len(job.Spec.Template.Spec.Containers))

	for _, c := range job.Spec.Template.Spec.Containers {
		isJsonObj := func(v string) (bool, error) {
			return json.Valid([]byte(v)) && strings.HasPrefix(v, "{"), nil
		}
		matchStr := func(str string) func(v string) (bool, error) {
			return func(v string) (bool, error) {
				return v == str, nil
			}
		}

		requireEnv(t, c.Env, "TEST", matchStr("test"))
		requireEnv(t, c.Env, "JOB", isJsonObj)
		requireEnv(t, c.Env, "JOB_metadata_name", matchStr("\"upgrade-1234-4-5-13\""))
		requireEnv(t, c.Env, "JOB_spec_desiredVersion_version", matchStr("\"4.5.13\""))
		requireEnv(t, c.Env, "JOB_spec_startAfter", matchStr("\"2022-12-04T15:45:00Z\""))
		requireEnv(t, c.Env, "JOB_status_conditions_0_type", matchStr("\"Started\""))
		requireEnv(t, c.Env, "EVENT", isJsonObj)
		requireEnv(t, c.Env, "EVENT_name", matchStr("\"Create\""))
		requireEnv(t, c.Env, "META", isJsonObj)
		requireEnv(t, c.Env, "META_matchesDisruptiveHooks", matchStr("false"))
	}
}

func Test_UpgradeJobReconciler_Reconcile_Disruptive(t *testing.T) {
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
			Labels:    map[string]string{"test": "test"},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{},
		Status: managedupgradev1beta1.UpgradeJobStatus{
			Conditions: []metav1.Condition{
				{
					Type:   managedupgradev1beta1.UpgradeJobConditionStarted,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	disruptiveJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "disruptive",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			FailurePolicy: managedupgradev1beta1.FailurePolicyAbort,
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventCreate,
			},
			Disruptive: true,
			Template: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
				},
			},
		},
	}
	otherJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			FailurePolicy: managedupgradev1beta1.FailurePolicyAbort,
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventCreate,
			},
			Disruptive: false,
			Template: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
				},
			},
		},
	}

	c := controllerClient(t, upgradeJob, disruptiveJobHook, otherJobHook,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			}})

	subject := &UpgradeJobReconciler{
		Client: c,
		Scheme: c.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	matchStr := func(str string) func(v string) (bool, error) {
		return func(v string) (bool, error) {
			return v == str, nil
		}
	}

	job := checkAndCompleteHook(t, c, subject, upgradeJob, disruptiveJobHook, managedupgradev1beta1.EventCreate, true)
	require.Equal(t, 1, len(job.Spec.Template.Spec.Containers))
	requireEnv(t, job.Spec.Template.Spec.Containers[0].Env, "META_matchesDisruptiveHooks", matchStr("true"))

	job = checkAndCompleteHook(t, c, subject, upgradeJob, otherJobHook, managedupgradev1beta1.EventCreate, true)
	require.Equal(t, 1, len(job.Spec.Template.Spec.Containers))
	requireEnv(t, job.Spec.Template.Spec.Containers[0].Env, "META_matchesDisruptiveHooks", matchStr("true"))
}

func Test_UpgradeJobReconciler_Reconcile_ClaimNextHook(t *testing.T) {
	ctx := context.Background()
	clock := mockClock{now: time.Date(2022, 12, 4, 22, 45, 0, 0, time.UTC)}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: managedupgradev1beta1.GroupVersion.String(),
			Kind:       "UpgradeJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
			Labels:    map[string]string{"test": "test"},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(-time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-7 * time.Hour)),
		},
	}
	upgradeJob2 := upgradeJob.DeepCopy()
	upgradeJob2.Name = "upgrade-1234-4-5-14"
	upgradeJobHook := &managedupgradev1beta1.UpgradeJobHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "notify",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobHookSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: upgradeJob.Labels,
			},
			Run: managedupgradev1beta1.RunNext,
			Events: []managedupgradev1beta1.UpgradeEvent{
				managedupgradev1beta1.EventCreate,
			},
		},
	}

	c := controllerClient(t, upgradeJob, upgradeJob2, upgradeJobHook,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			}})

	subject := &UpgradeJobReconciler{
		Client: c,
		Scheme: c.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)

	var jobs batchv1.JobList
	require.NoError(t, c.List(ctx, &jobs))
	require.Equal(t, 1, len(jobs.Items), "hook job should be created")

	expectedClaim := managedupgradev1beta1.ClaimReference{
		APIVersion: "managedupgrade.appuio.io/v1beta1",
		Kind:       "UpgradeJob",
		Name:       upgradeJob.Name,
		UID:        upgradeJob.UID,
	}

	require.NoError(t, c.Get(ctx, requestForObject(upgradeJobHook).NamespacedName, upgradeJobHook))
	require.Equal(t, expectedClaim, upgradeJobHook.Status.ClaimedBy, "hook should be claimed by upgrade job")

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)

	require.NoError(t, c.Get(ctx, requestForObject(upgradeJobHook).NamespacedName, upgradeJobHook))
	require.Equal(t, expectedClaim, upgradeJobHook.Status.ClaimedBy, "hook should still be claimed by upgrade job")

	require.NoError(t, c.List(ctx, &jobs))
	require.Equal(t, 1, len(jobs.Items), "no additional jobs should be created")
}

func requireEnv(t *testing.T, list []corev1.EnvVar, name string, valueMatcher func(string) (bool, error)) {
	t.Helper()

	for _, env := range list {
		if env.Name == name {
			ok, err := valueMatcher(env.Value)
			require.NoError(t, err)
			require.Truef(t, ok, "env %s has unexpected value %q", name, env.Value)
			return
		}
	}
	require.Failf(t, "env not found", "env %q not found", name)
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
			DesiredVersion: &configv1.Update{
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

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 10)

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
			DesiredVersion: &configv1.Update{
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
			DesiredVersion: &configv1.Update{
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
			DesiredVersion: &configv1.Update{
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

func Test_UpgradeJobReconciler_Reconcile_PausedMachineConfigPools(t *testing.T) {
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
					Status: configv1.ConditionFalse,
				},
			},
			History: []configv1.UpdateHistory{},
		},
	}

	masterPool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "master",
			Labels: map[string]string{"name": "master"},
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 0,
		},
	}
	storagePool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "storage",
			Labels: map[string]string{"name": "storage"},
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 3,
		},
	}
	workerPool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker",
			Labels: map[string]string{"name": "worker"},
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 0,
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(3 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Minute)),
			DesiredVersion: &configv1.Update{
				Version: "4.5.13",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
				MachineConfigPools: []managedupgradev1beta1.UpgradeJobMachineConfigPoolSpec{
					{
						MatchLabels: &metav1.LabelSelector{
							MatchLabels: map[string]string{"name": "worker"},
						},
						DelayUpgrade: managedupgradev1beta1.UpgradeJobMachineConfigPoolDelayUpgradeSpec{
							DelayMin: metav1.Duration{Duration: 1 * time.Hour},
						},
					},
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob, masterPool, storagePool, workerPool)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 10)
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	startedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted)
	require.NotNil(t, startedCond, "should have started upgrade")
	pausedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionMachineConfigPoolsPaused)
	require.NotNil(t, pausedCond, "should have paused mcp upgrades")
	require.Equal(t, metav1.ConditionTrue, pausedCond.Status)
	// pools
	require.NoError(t, client.Get(ctx, requestForObject(masterPool).NamespacedName, masterPool))
	require.False(t, masterPool.Spec.Paused, "should not have paused master mcp, since it does not match the selector")
	require.NoError(t, client.Get(ctx, requestForObject(workerPool).NamespacedName, workerPool))
	require.True(t, workerPool.Spec.Paused, "should have paused worker mcp")
	require.NoError(t, client.Get(ctx, requestForObject(storagePool).NamespacedName, storagePool))
	require.False(t, storagePool.Spec.Paused, "should not have paused storage mcp, since it does not match the selector")

	// finish the upgrade
	require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
	ucv.Status.History = append(ucv.Status.History, configv1.UpdateHistory{
		State:   configv1.CompletedUpdate,
		Version: upgradeJob.Spec.DesiredVersion.Version,
		Image:   upgradeJob.Spec.DesiredVersion.Image,
	})
	require.NoError(t, client.Status().Update(ctx, ucv))
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 5)
	// master pool still upgrading
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	requireJobNotInFinalState(t, *upgradeJob)
	upgradeCompletedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted)
	require.NotNil(t, upgradeCompletedCond)
	require.Equal(t, metav1.ConditionFalse, upgradeCompletedCond.Status)
	masterPool.Status.UpdatedMachineCount = masterPool.Status.MachineCount
	require.NoError(t, client.Status().Update(ctx, masterPool))

	// check that job becomes paused after partial completion
	lastResult := reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 5)
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	requireJobNotInFinalState(t, *upgradeJob)
	upgradeCompletedCond = apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionUpgradeCompleted)
	require.NotNil(t, upgradeCompletedCond)
	require.Equal(t, metav1.ConditionTrue, upgradeCompletedCond.Status)
	pausedCond = apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPaused)
	require.NotNil(t, pausedCond)
	require.Equal(t, metav1.ConditionTrue, pausedCond.Status)
	// requeue since we might not have any events to trigger the next step
	require.True(t, lastResult.Requeue)
	require.Equal(t, time.Hour-time.Minute, lastResult.RequeueAfter, "should requeue after the remaining delay relative to the start time")

	// advance time to the end of the delay
	clock.Advance(lastResult.RequeueAfter)
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	// check job conditions
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	requireJobNotInFinalState(t, *upgradeJob)
	pausedCond = apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionMachineConfigPoolsPaused)
	require.NotNil(t, pausedCond)
	require.Equal(t, metav1.ConditionFalse, pausedCond.Status)
	pausedCond = apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionPaused)
	require.NotNil(t, pausedCond)
	require.Equal(t, metav1.ConditionFalse, pausedCond.Status)
	// pools
	require.NoError(t, client.Get(ctx, requestForObject(workerPool).NamespacedName, workerPool))
	require.False(t, workerPool.Spec.Paused, "should have unpaused worker mcp")

	// finish pool upgrade
	workerPool.Status.UpdatedMachineCount = workerPool.Status.MachineCount
	require.NoError(t, client.Status().Update(ctx, workerPool))
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	succeededCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded)
	require.NotNil(t, succeededCond)
	require.Equal(t, metav1.ConditionTrue, succeededCond.Status)
}

func Test_UpgradeJobReconciler_Reconcile_PausedMachineConfigPools_UnpauseExpire(t *testing.T) {
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
					Status: configv1.ConditionFalse,
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

	workerPool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker",
			Labels: map[string]string{"name": "worker"},
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 0,
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(3 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Minute)),
			DesiredVersion: &configv1.Update{
				Version: "4.5.13",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
				MachineConfigPools: []managedupgradev1beta1.UpgradeJobMachineConfigPoolSpec{
					{
						MatchLabels: &metav1.LabelSelector{
							MatchLabels: map[string]string{"name": "worker"},
						},
						DelayUpgrade: managedupgradev1beta1.UpgradeJobMachineConfigPoolDelayUpgradeSpec{
							DelayMin: metav1.Duration{Duration: 1 * time.Hour},
							DelayMax: metav1.Duration{Duration: 2 * time.Hour},
						},
					},
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob, workerPool)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 10)
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	startedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted)
	require.NotNil(t, startedCond, "should have started upgrade")
	pausedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionMachineConfigPoolsPaused)
	require.NotNil(t, pausedCond, "should have paused mcp upgrades")
	require.Equal(t, metav1.ConditionTrue, pausedCond.Status)
	// pools
	require.NoError(t, client.Get(ctx, requestForObject(workerPool).NamespacedName, workerPool))
	require.True(t, workerPool.Spec.Paused, "should have paused worker mcp")

	// advance time to the end of the max delay
	clock.Advance(4 * time.Hour)
	_, err := subject.Reconcile(ctx, requestForObject(upgradeJob))
	require.Error(t, err, "should fail if max delay is exceeded")
	// check job conditions
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	failedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	require.NotNil(t, failedCond, "should set failed condition")
	require.Equal(t, metav1.ConditionTrue, failedCond.Status, "should set failed condition")
	require.Equal(t, managedupgradev1beta1.UpgradeJobReasonUnpausingPoolsExpired, failedCond.Reason, "should set reason to unpausing pools expired")
}

// Test_UpgradeJobReconciler_Reconcile_PausedMachineConfigPools_EnsureUnpause tests that the upgrade job reconciler
// will unpause machine config pools at the end of an upgrade even if they did not require any upgrades
func Test_UpgradeJobReconciler_Reconcile_PausedMachineConfigPools_EnsureUnpause(t *testing.T) {
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
					Status: configv1.ConditionFalse,
				},
			},
		},
	}

	workerPool := &machineconfigurationv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker",
			Labels: map[string]string{"name": "worker"},
		},
		Status: machineconfigurationv1.MachineConfigPoolStatus{
			MachineCount:        3,
			UpdatedMachineCount: 3,
		},
	}

	upgradeJob := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrade-1234-4-5-13",
			Namespace: "appuio-openshift-upgrade-controller",
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartBefore: metav1.NewTime(clock.Now().Add(3 * time.Hour)),
			StartAfter:  metav1.NewTime(clock.Now().Add(-time.Minute)),
			DesiredVersion: &configv1.Update{
				Version: "4.5.13",
			},
			UpgradeJobConfig: managedupgradev1beta1.UpgradeJobConfig{
				UpgradeTimeout: metav1.Duration{Duration: 12 * time.Hour},
				MachineConfigPools: []managedupgradev1beta1.UpgradeJobMachineConfigPoolSpec{
					{
						MatchLabels: &metav1.LabelSelector{
							MatchLabels: map[string]string{"name": "worker"},
						},
						DelayUpgrade: managedupgradev1beta1.UpgradeJobMachineConfigPoolDelayUpgradeSpec{
							DelayMin: metav1.Duration{Duration: 1 * time.Hour},
							DelayMax: metav1.Duration{Duration: 2 * time.Hour},
						},
					},
				},
			},
		},
	}

	client := controllerClient(t, ucv, upgradeJob, workerPool)

	subject := &UpgradeJobReconciler{
		Client: client,
		Scheme: client.Scheme(),

		Clock: &clock,

		ManagedUpstreamClusterVersionName: "version",
	}

	t.Log("check that upgrade job is started and machine config pools are paused")
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 10)
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	startedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionStarted)
	require.NotNil(t, startedCond, "should have started upgrade")
	pausedCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionMachineConfigPoolsPaused)
	require.NotNil(t, pausedCond, "should have paused mcp upgrades")
	require.Equal(t, metav1.ConditionTrue, pausedCond.Status)
	require.NoError(t, client.Get(ctx, requestForObject(workerPool).NamespacedName, workerPool))
	require.True(t, workerPool.Spec.Paused, "should have paused worker mcp")

	t.Log("finish the upgrade")
	require.NoError(t, client.Get(ctx, requestForObject(ucv).NamespacedName, ucv))
	ucv.Status.History = append(ucv.Status.History, configv1.UpdateHistory{
		State:   configv1.CompletedUpdate,
		Version: upgradeJob.Spec.DesiredVersion.Version,
		Image:   upgradeJob.Spec.DesiredVersion.Image,
	})
	require.NoError(t, client.Status().Update(ctx, ucv))
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 5)

	t.Log("check that job is done and ensure machine config pools are unpaused")
	require.NoError(t, client.Get(ctx, requestForObject(upgradeJob).NamespacedName, upgradeJob))
	succeededCond := apimeta.FindStatusCondition(upgradeJob.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded)
	require.NotNil(t, succeededCond)
	require.Equal(t, metav1.ConditionTrue, succeededCond.Status)
	require.NoError(t, client.Get(ctx, requestForObject(workerPool).NamespacedName, workerPool))
	require.False(t, workerPool.Spec.Paused, "should have unpaused worker mcp for completed job")
}

func Test_JobFromClusterVersionHandler(t *testing.T) {
	ucv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}

	client := controllerClient(t, ucv)
	subject := JobFromClusterVersionMapper(client, "version")

	require.Len(t, subject(context.Background(), nil), 0, "should not return a reconcile request if clusterversion is not locked")

	ucv.Annotations = map[string]string{
		ClusterVersionLockAnnotation: "ns/upgrade-1234-4-5-13",
	}
	require.NoError(t, client.Update(context.Background(), ucv))

	reqs := subject(context.Background(), nil)
	require.Len(t, reqs, 1, "should return a reconcile request if clusterversion is locked")
	require.Equal(t, types.NamespacedName{Namespace: "ns", Name: "upgrade-1234-4-5-13"}, reqs[0].NamespacedName)
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
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, machineconfigurationv1.AddToScheme(scheme))
	require.NoError(t, managedupgradev1beta1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initObjs...).
		WithStatusSubresource(
			&managedupgradev1beta1.UpgradeConfig{},
			&managedupgradev1beta1.UpgradeJob{},
			&managedupgradev1beta1.UpgradeJobHook{},
			&managedupgradev1beta1.ClusterVersion{},
			&configv1.ClusterVersion{},
			&batchv1.Job{},
			&machineconfigurationv1.MachineConfigPool{},
		).
		Build()
}

func checkAndCompleteHook(t *testing.T, c client.WithWatch, subject *UpgradeJobReconciler, upgradeJob *managedupgradev1beta1.UpgradeJob, upgradeJobHook *managedupgradev1beta1.UpgradeJobHook, event managedupgradev1beta1.UpgradeEvent, fail bool) batchv1.Job {
	t.Helper()
	ctx := context.Background()

	var jobs batchv1.JobList

	sel := client.MatchingLabels{
		hookJobTrackingLabelUpgradeJobHook: upgradeJobHook.Name,
		hookJobTrackingLabelUpgradeJob:     upgradeJob.Name,
		hookJobTrackingLabelEvent:          string(event),
	}

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	require.NoError(t, c.List(ctx, &jobs, sel))
	require.Lenf(t, jobs.Items, 1, "should create a job with %q labels", sel)
	require.Contains(t, jobs.Items[0].Finalizers, UpgradeJobHookJobTrackerFinalizer, "should add finalizer to job")

	ct := batchv1.JobComplete
	if fail {
		ct = batchv1.JobFailed
	}
	expectedTrackedStatus := managedupgradev1beta1.HookJobTrackerStatusComplete
	if fail {
		expectedTrackedStatus = managedupgradev1beta1.HookJobTrackerStatusFailed
	}

	job := jobs.Items[0]
	job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
		Type:   ct,
		Status: corev1.ConditionTrue,
	})
	require.NoError(t, c.Status().Update(ctx, &job))

	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	require.NoError(t, c.List(ctx, &jobs, sel))
	require.Lenf(t, jobs.Items, 1, "should create a job with %q labels", sel)
	require.NotContains(t, jobs.Items[0].Finalizers, UpgradeJobHookJobTrackerFinalizer, "should have removed finalizer from job after completion")
	var uj managedupgradev1beta1.UpgradeJob
	require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, &uj))
	tracked := findTrackedHookJob(upgradeJobHook.Name, string(event), uj)
	require.Len(t, tracked, 1, "should have tracked hook job")
	require.Equal(t, expectedTrackedStatus, tracked[0].Status, "should track correct state")

	// People might cleanup finished jobs or the TTL controller deletes them https://kubernetes.io/docs/concepts/workloads/controllers/ttlafterfinished/
	// The controller should not fail in this case or try to recreate the job
	require.NoError(t, c.Delete(ctx, &job))
	reconcileNTimes(t, subject, ctx, requestForObject(upgradeJob), 3)
	require.NoError(t, c.Get(ctx, requestForObject(upgradeJob).NamespacedName, &uj))
	tracked = findTrackedHookJob(upgradeJobHook.Name, string(event), uj)
	require.Len(t, tracked, 1, "should still have completed tracked hook job")
	require.Equal(t, expectedTrackedStatus, tracked[0].Status, "should have kept the state")
	require.NoError(t, c.List(ctx, &jobs, sel))
	require.Lenf(t, jobs.Items, 0, "should not have recreated the deleted job")

	return job
}

// requireJobNotInFinalState checks that the upgrade job is not in a final state
func requireJobNotInFinalState(t *testing.T, uj managedupgradev1beta1.UpgradeJob) {
	t.Helper()

	succeededCond := apimeta.FindStatusCondition(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionSucceeded)
	if succeededCond != nil {
		require.NotEqual(t, metav1.ConditionTrue, succeededCond.Status, "job should not be in final state but succeeded")
	}
	failedCond := apimeta.FindStatusCondition(uj.Status.Conditions, managedupgradev1beta1.UpgradeJobConditionFailed)
	if failedCond != nil {
		require.NotEqual(t, metav1.ConditionTrue, failedCond.Status, "job should not be in final state but failed")
	}
}
