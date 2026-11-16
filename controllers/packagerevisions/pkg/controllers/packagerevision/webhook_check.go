// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package packagerevision

import (
	"context"
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// validatingWebhookConfigName must match the name in
	// controllers/packagerevisions/config/webhook/manifests.yaml.
	validatingWebhookConfigName = "packagerevision-validating-webhook-configuration"
	// packageRevisionValidatorName is the webhook entry that guards v1alpha2 deletes.
	packageRevisionValidatorName = "packagerevision-validator.porch.kpt.dev"
)

//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get

// verifyWebhookRegistration checks that the PackageRevision validating webhook is
// registered. Without it, deletes and proposals of referenced v1alpha2 packages are
// not blocked, and nothing else reports that.
func verifyWebhookRegistration(ctx context.Context, reader client.Reader) error {
	cfg := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	if err := reader.Get(ctx, client.ObjectKey{Name: validatingWebhookConfigName}, cfg); err != nil {
		return fmt.Errorf("cannot read validating webhook configuration %q: %w", validatingWebhookConfigName, err)
	}

	for i := range cfg.Webhooks {
		wh := &cfg.Webhooks[i]
		if wh.Name != packageRevisionValidatorName {
			continue
		}
		if wh.FailurePolicy != nil && *wh.FailurePolicy == admissionregistrationv1.Ignore {
			return fmt.Errorf("webhook %q has failurePolicy Ignore: deletes are not guarded when it is unavailable",
				packageRevisionValidatorName)
		}
		return nil
	}

	return fmt.Errorf("webhook %q is not in configuration %q: deletes of referenced v1alpha2 packages are not blocked",
		packageRevisionValidatorName, validatingWebhookConfigName)
}
