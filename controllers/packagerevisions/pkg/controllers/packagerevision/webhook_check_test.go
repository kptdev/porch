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
	"testing"

	"github.com/stretchr/testify/assert"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func webhookConfig(webhookName string, policy *admissionregistrationv1.FailurePolicyType) *admissionregistrationv1.ValidatingWebhookConfiguration {
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfigName},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: webhookName, FailurePolicy: policy},
		},
	}
}

func TestVerifyWebhookRegistration(t *testing.T) {
	fail := admissionregistrationv1.Fail
	ignore := admissionregistrationv1.Ignore

	tests := []struct {
		name    string
		objs    []*admissionregistrationv1.ValidatingWebhookConfiguration
		wantErr string
	}{
		{
			name: "registered with failurePolicy Fail",
			objs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				webhookConfig(packageRevisionValidatorName, &fail),
			},
		},
		{
			name: "registered with default failurePolicy",
			objs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				webhookConfig(packageRevisionValidatorName, nil),
			},
		},
		{
			name:    "configuration missing",
			wantErr: "cannot read validating webhook configuration",
		},
		{
			name: "configuration present without this validator",
			objs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				webhookConfig("some-other-webhook.example.com", &fail),
			},
			wantErr: "is not in configuration",
		},
		{
			name: "failurePolicy Ignore leaves deletes unguarded",
			objs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				webhookConfig(packageRevisionValidatorName, &ignore),
			},
			wantErr: "failurePolicy Ignore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder()
			for _, obj := range tt.objs {
				builder = builder.WithObjects(obj)
			}

			err := verifyWebhookRegistration(context.Background(), builder.Build())
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
