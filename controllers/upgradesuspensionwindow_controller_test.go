package controllers

import (
	"context"
	"testing"
	"time"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Test_UpgradeSuspensionWindowReconciler_Reconcile(t *testing.T) {
	ctx := context.Background()

	j1 := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "before",
			Namespace: "testns",
			Labels: map[string]string{
				"test": "test",
			},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter: metav1.NewTime(time.Date(2022, 2, 1, 0, 0, 0, 0, time.UTC)),
		},
	}
	j2 := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "during",
			Namespace: "testns",
			Labels: map[string]string{
				"test": "test",
			},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter: metav1.NewTime(time.Date(2022, 4, 1, 0, 0, 0, 0, time.UTC)),
		},
	}
	j3 := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "after",
			Namespace: "testns",
			Labels: map[string]string{
				"test": "test",
			},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter: metav1.NewTime(time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)),
		},
	}
	j4 := &managedupgradev1beta1.UpgradeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "during-wrong-label",
			Namespace: "testns",
			Labels: map[string]string{
				"other": "other",
			},
		},
		Spec: managedupgradev1beta1.UpgradeJobSpec{
			StartAfter: metav1.NewTime(time.Date(2022, 4, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	cnf1 := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "matching",
			Namespace: "testns",
			Labels: map[string]string{
				"test": "test",
			},
		},
	}
	cnf2 := &managedupgradev1beta1.UpgradeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "not-matching",
			Namespace: "testns",
			Labels: map[string]string{
				"other": "other",
			},
		},
	}

	usw := &managedupgradev1beta1.UpgradeSuspensionWindow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
		Spec: managedupgradev1beta1.UpgradeSuspensionWindowSpec{
			Start: metav1.NewTime(time.Date(2022, 3, 1, 0, 0, 0, 0, time.UTC)),
			End:   metav1.NewTime(time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)),

			ConfigSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "test",
				},
			},
			JobSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "test",
				},
			},
		},
	}

	c := controllerClient(t, usw, j1, j2, j3, j4, cnf1, cnf2)

	subject := UpgradeSuspensionWindowReconciler{
		Client: c,
		Scheme: c.Scheme(),
	}

	_, err := subject.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(usw)})
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(usw), usw))

	assert.Equal(t, []managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject{{Name: "matching"}}, usw.Status.MatchingConfigs)
	assert.Equal(t, []managedupgradev1beta1.UpgradeSuspensionWindowStatusMatchingObject{{Name: "during"}}, usw.Status.MatchingJobs)
}
