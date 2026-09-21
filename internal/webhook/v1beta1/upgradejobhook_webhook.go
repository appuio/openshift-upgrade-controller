package v1beta1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	upgradejobhookv1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func SetupUpgradeJobHookWebhookWithManager(mgr ctrl.Manager) error {
	client := kubernetes.NewForConfigOrDie(mgr.GetConfig())
	return ctrl.NewWebhookManagedBy(mgr, &upgradejobhookv1beta1.UpgradeJobHook{}).
		WithValidator(NewUpgradeJobHookCustomValidator(client)).
		Complete()
}

//+kubebuilder:webhook:path=/validate-managedupgrade-appuio-io-v1beta1-upgradejobhook,mutating=false,failurePolicy=fail,sideEffects=None,groups=managedupgrade.appuio.io,resources=upgradejobhooks,verbs=create;update,versions=v1beta1,name=validate-managedupgrade-v1beta1-upgradejobhook.appuio.io,admissionReviewVersions=v1
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=create

// UpgradeJobHookCustomValidator validates UpgradeJobHook writes by asking
// the apiserver — via a server-side dry-run create — whether it would admit
// a Job built from the hook's embedded template.
type UpgradeJobHookCustomValidator struct {
	client kubernetes.Interface
}

// NewUpgradeJobHookCustomValidator builds a validator with its own client
// (proper dependency injection).
func NewUpgradeJobHookCustomValidator(client kubernetes.Interface) *UpgradeJobHookCustomValidator {
	return &UpgradeJobHookCustomValidator{client: client}
}

var _ admission.Validator[*upgradejobhookv1beta1.UpgradeJobHook] = &UpgradeJobHookCustomValidator{}

func (v *UpgradeJobHookCustomValidator) ValidateCreate(ctx context.Context, hook *upgradejobhookv1beta1.UpgradeJobHook) (admission.Warnings, error) {
	l := log.FromContext(ctx).WithName("UpgradeJobHook.Validation")
	l.Info("validate create", "name", hook.Name)
	return nil, v.validate(ctx, hook)
}

// ValidateUpdate implements admission.Validator.
func (v *UpgradeJobHookCustomValidator) ValidateUpdate(ctx context.Context, oldHook, newHook *upgradejobhookv1beta1.UpgradeJobHook) (admission.Warnings, error) {
	l := log.FromContext(ctx).WithName("UpgradeJobHook.Validation")
	l.Info("validate update")

	// Skip the dry-run when the template is untouched — this covers the
	// controller's own status/finalizer PATCHes. Caveat: hooks that predate
	// this webhook stay unvalidated until their spec actually changes.
	if apiequality.Semantic.DeepEqual(oldHook.Spec.Template, newHook.Spec.Template) {
		return nil, nil
	}
	return nil, v.validate(ctx, newHook)
}

// ValidateDelete implements admission.Validator — deletion is not validated
// (the webhook marker only registers verbs=create;update).
func (v *UpgradeJobHookCustomValidator) ValidateDelete(_ context.Context, _ *upgradejobhookv1beta1.UpgradeJobHook) (admission.Warnings, error) {
	return nil, nil
}

// validate asks the apiserver: "would you admit a Job with this spec, here,
// now?" — via a server-side dry-run create. The full Job create chain runs
// (defaulting, selector generation, validation, quota, other webhooks) and
// nothing is persisted.
func (v *UpgradeJobHookCustomValidator) validate(ctx context.Context, r *upgradejobhookv1beta1.UpgradeJobHook) error {
	l := log.FromContext(ctx).WithName("UpgradeJobHook.validate")

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: r.Name,
			Namespace:    r.Namespace,
			// The JobTemplateSpec's metadata is part of the embedded manifest —
			// copy labels/annotations so the apiserver validates them too (a
			// bad value here would fail the controller's real Job create).
			// metadata.name is deliberately NOT copied: generateName governs,
			// and the real Job's name is the controller's business.
			Labels:      r.Spec.Template.Labels,
			Annotations: r.Spec.Template.Annotations,
		},
		// Template.Spec is a batchv1.JobSpec value (JobTemplateSpec is the
		// canonical embed-a-Job type, as used by CronJob's jobTemplate).
		// The whole value — pod template and its metadata included — rides
		// along. An absent template is just the zero JobSpec: let it flow,
		// the apiserver reports exactly what's missing, remapped to
		// CR-relative paths.
		Spec: r.Spec.Template.Spec,
	}

	cctx, cancel := context.WithTimeout(ctx, 4*time.Second) // own budget inside the webhook deadline
	defer cancel()

	_, err := v.client.BatchV1().Jobs(job.Namespace).Create(cctx, job,
		metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if err == nil {
		return nil // the apiserver would accept this Job as-is
	}

	switch {
	case apierrors.IsInvalid(err):
		var upstreamCauses []metav1.StatusCause
		statusErr, ok := errors.AsType[*apierrors.StatusError](err)
		if ok && statusErr.Status().Details != nil {
			upstreamCauses = statusErr.Status().Details.Causes
		}
		causes := make([]metav1.StatusCause, 0, len(upstreamCauses))
		for _, c := range upstreamCauses {
			causes = append(causes, metav1.StatusCause{
				Type:    c.Type,
				Message: c.Message,
				Field:   "spec.template." + c.Field,
			})
		}
		l.Info("denied: job template invalid", "causes", causes, "upstream_error", err)
		return &apierrors.StatusError{ErrStatus: metav1.Status{
			Code:    http.StatusUnprocessableEntity,
			Reason:  metav1.StatusReasonInvalid,
			Message: fmt.Sprintf("Job creation from template would fail. (Upstream error: %s)", err.Error()),
			Details: &metav1.StatusDetails{Causes: causes},
		}}
	default:
		return fmt.Errorf("Job creation from template would fail: %w", err)
	}
}
