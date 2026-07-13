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

package webhooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	admissionv1 "k8s.io/api/admission/v1"
)

// TestUnmarshalPackageRevisionEdgeCases tests JSON unmarshaling with edge cases.
func TestUnmarshalPackageRevisionEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		rawBytes []byte
		wantErr  bool
	}{
		{
			name:     "valid object",
			rawBytes: []byte(`{"apiVersion":"porch.kpt.dev/v1alpha2","kind":"PackageRevision","metadata":{"name":"test"}}`),
			wantErr:  false,
		},
		{
			name:     "empty bytes",
			rawBytes: []byte{},
			wantErr:  true,
		},
		{
			name:     "nil bytes",
			rawBytes: nil,
			wantErr:  true,
		},
		{
			name:     "malformed JSON",
			rawBytes: []byte(`{invalid json}`),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &v1alpha2.PackageRevision{}
			err := json.Unmarshal(tt.rawBytes, pr)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSourceEqual tests source equality comparisons.
func TestSourceEqual(t *testing.T) {
	tests := []struct {
		name  string
		s1    *v1alpha2.PackageSource
		s2    *v1alpha2.PackageSource
		equal bool
	}{
		{
			name:  "both nil",
			s1:    nil,
			s2:    nil,
			equal: true,
		},
		{
			name:  "one nil one not",
			s1:    nil,
			s2:    &v1alpha2.PackageSource{},
			equal: false,
		},
		{
			name: "identical Init",
			s1: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test", Keywords: []string{"a", "b"}, Site: "site"},
			},
			s2: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test", Keywords: []string{"a", "b"}, Site: "site"},
			},
			equal: true,
		},
		{
			name: "different Init description",
			s1: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test1"},
			},
			s2: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test2"},
			},
			equal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sourceEqual(tt.s1, tt.s2)
			assert.Equal(t, tt.equal, result)
		})
	}
}

// TestContainsLifecycle tests lifecycle containment check.
func TestContainsLifecycle(t *testing.T) {
	tests := []struct {
		name       string
		lifecycles []v1alpha2.PackageRevisionLifecycle
		target     v1alpha2.PackageRevisionLifecycle
		contains   bool
	}{
		{
			name:       "empty list",
			lifecycles: []v1alpha2.PackageRevisionLifecycle{},
			target:     v1alpha2.PackageRevisionLifecycleDraft,
			contains:   false,
		},
		{
			name:       "target present",
			lifecycles: []v1alpha2.PackageRevisionLifecycle{v1alpha2.PackageRevisionLifecycleDraft},
			target:     v1alpha2.PackageRevisionLifecycleDraft,
			contains:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsLifecycle(tt.lifecycles, tt.target)
			assert.Equal(t, tt.contains, result)
		})
	}
}

// TestValidateRepositoryAnnotation tests repository annotation validation.
func TestValidateRepositoryAnnotation(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	tests := []struct {
		name    string
		setup   func() (client.Reader, *v1alpha2.PackageRevision)
		wantErr bool
	}{
		{
			name: "valid repository with annotation",
			setup: func() (client.Reader, *v1alpha2.PackageRevision) {
				repo := &configapi.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-repo",
						Namespace: "default",
						Annotations: map[string]string{
							configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
						},
					},
				}
				client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
				pr := &v1alpha2.PackageRevision{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
					Spec:       v1alpha2.PackageRevisionSpec{RepositoryName: "test-repo"},
				}
				return client, pr
			},
			wantErr: false,
		},
		{
			name: "repository not found",
			setup: func() (client.Reader, *v1alpha2.PackageRevision) {
				client := fake.NewClientBuilder().WithScheme(scheme).Build()
				pr := &v1alpha2.PackageRevision{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
					Spec:       v1alpha2.PackageRevisionSpec{RepositoryName: "nonexistent"},
				}
				return client, pr
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, pr := tt.setup()
			validator := NewPackageRevisionValidator(client)
			err := validator.validateRepositoryAnnotation(context.Background(), pr)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateLifecycleTransition tests lifecycle transition validation.
func TestValidateLifecycleTransition(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)

	tests := []struct {
		name    string
		from    v1alpha2.PackageRevisionLifecycle
		to      v1alpha2.PackageRevisionLifecycle
		wantErr bool
	}{
		{
			name:    "Draft to Proposed - valid",
			from:    v1alpha2.PackageRevisionLifecycleDraft,
			to:      v1alpha2.PackageRevisionLifecycleProposed,
			wantErr: false,
		},
		{
			name:    "Draft to Published - invalid",
			from:    v1alpha2.PackageRevisionLifecycleDraft,
			to:      v1alpha2.PackageRevisionLifecyclePublished,
			wantErr: true,
		},
		{
			name:    "Proposed to Published - valid",
			from:    v1alpha2.PackageRevisionLifecycleProposed,
			to:      v1alpha2.PackageRevisionLifecyclePublished,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateLifecycleTransition(tt.from, tt.to)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateRenderRacePrevention tests render race prevention.
func TestValidateRenderRacePrevention(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)

	tests := []struct {
		name    string
		pr      *v1alpha2.PackageRevision
		wantErr bool
	}{
		{
			name: "rendering in progress",
			pr: &v1alpha2.PackageRevision{
				Status: v1alpha2.PackageRevisionStatus{
					RenderingPrrResourceVersion: "v1",
				},
			},
			wantErr: true,
		},
		{
			name: "render complete",
			pr: &v1alpha2.PackageRevision{
				Status: v1alpha2.PackageRevisionStatus{
					ObservedPrrResourceVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						v1alpha2.AnnotationRenderRequest: "v1",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateRenderRacePrevention(tt.pr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateImmutableFields tests immutable field validation.
func TestValidateImmutableFields(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)
	basePR := &v1alpha2.PackageRevision{
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
		},
	}

	tests := []struct {
		name    string
		oldPR   *v1alpha2.PackageRevision
		newPR   *v1alpha2.PackageRevision
		wantErr bool
	}{
		{
			name:    "no changes",
			oldPR:   basePR,
			newPR:   basePR,
			wantErr: false,
		},
		{
			name:  "repository changed",
			oldPR: basePR,
			newPR: &v1alpha2.PackageRevision{
				Spec: v1alpha2.PackageRevisionSpec{
					RepositoryName: "repo2",
					PackageName:    "pkg",
					WorkspaceName:  "ws",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateImmutableFields(tt.oldPR, tt.newPR)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestHandlePackageRevisionWebhookCreate tests CREATE operation handling.
func TestHandlePackageRevisionWebhookCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	rawBytes, _ := json.Marshal(pr)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed, "expected CREATE to be allowed: %s", resp.Result.Message)
}

// TestHandlePackageRevisionWebhookCreateMissingRepository tests CREATE rejection when repo not found.
func TestHandlePackageRevisionWebhookCreateMissingRepository(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := NewPackageRevisionValidator(client)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "nonexistent",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	rawBytes, _ := json.Marshal(pr)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "not found")
}

// TestHandlePackageRevisionWebhookUpdate tests UPDATE operation handling.
func TestHandlePackageRevisionWebhookUpdate(t *testing.T) {
	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	oldBytes, _ := json.Marshal(oldPR)
	newBytes, _ := json.Marshal(newPR)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: oldBytes},
			Object:    runtime.RawExtension{Raw: newBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed, "expected UPDATE to be allowed: %s", resp.Result.Message)
}

// TestHandlePackageRevisionWebhookDelete tests DELETE operation handling.
func TestHandlePackageRevisionWebhookDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default", UID: "uid-123"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	rawBytes, _ := json.Marshal(pr)
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().WithScheme(scheme).Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			OldObject: runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandlePackageRevisionWebhookDeleteWithReference tests DELETE rejection when referenced by other packages.
func TestHandlePackageRevisionWebhookDeleteWithReference(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	prToDelete := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "upstream-pr", Namespace: "default", UID: "uid-upstream"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "upstream",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	dependentPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream-pr", Namespace: "default", UID: "uid-downstream"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "downstream",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
			Source: &v1alpha2.PackageSource{
				CopyFrom: &v1alpha2.PackageRevisionRef{Name: "upstream-pr"},
			},
		},
	}

	rawBytes, _ := json.Marshal(prToDelete)

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dependentPR).Build()
	validator := NewPackageRevisionValidator(client)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			OldObject: runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "referenced")
}

// TestInitEqual tests PackageInitSpec equality.
func TestInitEqual(t *testing.T) {
	tests := []struct {
		name  string
		i1    *v1alpha2.PackageInitSpec
		i2    *v1alpha2.PackageInitSpec
		equal bool
	}{
		{
			name:  "both nil",
			i1:    nil,
			i2:    nil,
			equal: true,
		},
		{
			name:  "one nil one not",
			i1:    nil,
			i2:    &v1alpha2.PackageInitSpec{},
			equal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := initEqual(tt.i1, tt.i2)
			assert.Equal(t, tt.equal, result)
		})
	}
}

// TestCloneFromEqual tests UpstreamPackage equality for CloneFrom.
func TestCloneFromEqual(t *testing.T) {
	tests := []struct {
		name  string
		c1    *v1alpha2.UpstreamPackage
		c2    *v1alpha2.UpstreamPackage
		equal bool
	}{
		{
			name:  "both nil",
			c1:    nil,
			c2:    nil,
			equal: true,
		},
		{
			name:  "one nil one not",
			c1:    nil,
			c2:    &v1alpha2.UpstreamPackage{},
			equal: false,
		},
		{
			name:  "identical UpstreamRef",
			c1:    &v1alpha2.UpstreamPackage{UpstreamRef: &v1alpha2.PackageRevisionRef{Name: "upstream"}},
			c2:    &v1alpha2.UpstreamPackage{UpstreamRef: &v1alpha2.PackageRevisionRef{Name: "upstream"}},
			equal: true,
		},
		{
			name:  "different UpstreamRef names",
			c1:    &v1alpha2.UpstreamPackage{UpstreamRef: &v1alpha2.PackageRevisionRef{Name: "upstream1"}},
			c2:    &v1alpha2.UpstreamPackage{UpstreamRef: &v1alpha2.PackageRevisionRef{Name: "upstream2"}},
			equal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cloneFromEqual(tt.c1, tt.c2)
			assert.Equal(t, tt.equal, result)
		})
	}
}

// TestUpgradeEqual tests PackageUpgradeSpec equality.
func TestUpgradeEqual(t *testing.T) {
	tests := []struct {
		name  string
		u1    *v1alpha2.PackageUpgradeSpec
		u2    *v1alpha2.PackageUpgradeSpec
		equal bool
	}{
		{
			name:  "both nil",
			u1:    nil,
			u2:    nil,
			equal: true,
		},
		{
			name:  "one nil one not",
			u1:    nil,
			u2:    &v1alpha2.PackageUpgradeSpec{},
			equal: false,
		},
		{
			name: "identical specs",
			u1: &v1alpha2.PackageUpgradeSpec{
				OldUpstream:    v1alpha2.PackageRevisionRef{Name: "old"},
				NewUpstream:    v1alpha2.PackageRevisionRef{Name: "new"},
				CurrentPackage: v1alpha2.PackageRevisionRef{Name: "current"},
				Strategy:       "fast-forward",
			},
			u2: &v1alpha2.PackageUpgradeSpec{
				OldUpstream:    v1alpha2.PackageRevisionRef{Name: "old"},
				NewUpstream:    v1alpha2.PackageRevisionRef{Name: "new"},
				CurrentPackage: v1alpha2.PackageRevisionRef{Name: "current"},
				Strategy:       "fast-forward",
			},
			equal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := upgradeEqual(tt.u1, tt.u2)
			assert.Equal(t, tt.equal, result)
		})
	}
}

// TestHandleCreate tests Handle method with CREATE operation.
func TestHandleCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	rawBytes, _ := json.Marshal(pr)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleCreateEmptyObject tests Handle rejects empty CREATE object.
func TestHandleCreateEmptyObject(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte{}},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "empty")
}

// TestHandleCreateMalformedJSON tests Handle rejects malformed CREATE JSON.
func TestHandleCreateMalformedJSON(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte(`{invalid}`)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "unmarshal")
}

// TestHandleUpdate tests Handle method with UPDATE operation.
func TestHandleUpdate(t *testing.T) {
	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	oldBytes, _ := json.Marshal(oldPR)
	newBytes, _ := json.Marshal(newPR)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: oldBytes},
			Object:    runtime.RawExtension{Raw: newBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleUpdateEmptyNewObject tests Handle rejects empty UPDATE new object.
func TestHandleUpdateEmptyNewObject(t *testing.T) {
	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
	}
	oldBytes, _ := json.Marshal(oldPR)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: oldBytes},
			Object:    runtime.RawExtension{Raw: []byte{}},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "empty")
}

// TestHandleUpdateValidTransition tests Handle allows valid UPDATE transition.
func TestHandleUpdateValidTransition(t *testing.T) {
	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleProposed,
		},
	}
	oldBytes, _ := json.Marshal(oldPR)
	newBytes, _ := json.Marshal(newPR)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: oldBytes},
			Object:    runtime.RawExtension{Raw: newBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleDelete tests Handle method with DELETE operation.
func TestHandleDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default", UID: "uid-123"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	rawBytes, _ := json.Marshal(pr)
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().WithScheme(scheme).Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			OldObject: runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleDeleteEmptyObject tests Handle rejects empty DELETE object.
func TestHandleDeleteEmptyObject(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			OldObject: runtime.RawExtension{Raw: []byte{}},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "empty")
}

// TestHandleUnknownOperation tests Handle allows unknown operations.
func TestHandleUnknownOperation(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Connect,
			Object:    runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestValidateCreateInvalidLifecycle tests ValidateCreate rejects invalid lifecycle for user-created packages.
func TestValidateCreateInvalidLifecycle(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	// User-created package (has Source) with invalid lifecycle
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecyclePublished,
			Source: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test"},
			},
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Draft or Proposed")
}

// TestValidateCreateMissingLifecycle tests ValidateCreate rejects missing lifecycle for user-created packages.
func TestValidateCreateMissingLifecycle(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	// User-created package (has Source) without lifecycle
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Source: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test"},
			},
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "lifecycle is required")
}

// TestValidateCreateRepoDiscoveredPublished tests ValidateCreate allows Published for repo-discovered packages.
func TestValidateCreateRepoDiscoveredPublished(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	// Repo-discovered package (Source=nil) with Published lifecycle
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecyclePublished,
			// Source is nil - repo-discovered
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateCreateRepoDiscoveredDeletionProposed tests ValidateCreate allows DeletionProposed for repo-discovered packages.
func TestValidateCreateRepoDiscoveredDeletionProposed(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	// Repo-discovered package (Source=nil) with DeletionProposed lifecycle
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDeletionProposed,
			// Source is nil - repo-discovered
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateCreateUserCreatedDraft tests ValidateCreate accepts Draft for user-created packages.
func TestValidateCreateUserCreatedDraft(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	// User-created package (has Source) with Draft lifecycle
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws1",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
			Source: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test"},
			},
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateCreateUserCreatedProposed tests ValidateCreate accepts Proposed for user-created packages.
func TestValidateCreateUserCreatedProposed(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	// User-created package (has Source) with Proposed lifecycle
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws2",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleProposed,
			Source: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test"},
			},
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateCreateUserCreatedMissingRepository tests ValidateCreate rejects missing repository for user-created packages.
func TestValidateCreateUserCreatedMissingRepository(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := NewPackageRevisionValidator(client)

	// User-created package with missing repository
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "nonexistent",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
			Source: &v1alpha2.PackageSource{
				Init: &v1alpha2.PackageInitSpec{Description: "test"},
			},
		},
	}

	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "repository validation failed")
}

// TestValidateUpdateInvalidTransition tests ValidateUpdate rejects invalid lifecycle transition.
func TestValidateUpdateInvalidTransition(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	oldPR := &v1alpha2.PackageRevision{
		Spec: v1alpha2.PackageRevisionSpec{Lifecycle: v1alpha2.PackageRevisionLifecyclePublished},
	}
	newPR := &v1alpha2.PackageRevision{
		Spec: v1alpha2.PackageRevisionSpec{Lifecycle: v1alpha2.PackageRevisionLifecycleDraft},
	}

	_, err := validator.ValidateUpdate(context.Background(), oldPR, newPR)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid transition")
}

// TestValidateUpdateNoLifecycleChange tests ValidateUpdate allows when lifecycle unchanged.
func TestValidateUpdateNoLifecycleChange(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	oldPR := &v1alpha2.PackageRevision{
		Spec: v1alpha2.PackageRevisionSpec{Lifecycle: v1alpha2.PackageRevisionLifecycleDraft},
	}
	newPR := &v1alpha2.PackageRevision{
		Spec: v1alpha2.PackageRevisionSpec{Lifecycle: v1alpha2.PackageRevisionLifecycleDraft},
	}

	_, err := validator.ValidateUpdate(context.Background(), oldPR, newPR)
	assert.NoError(t, err)
}

// TestValidateWorkspaceUniqueness tests workspace uniqueness validation.
func TestValidateWorkspaceUniqueness(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)

	existingPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-pr",
			Namespace: "default",
			Labels: map[string]string{
				v1alpha2.RepositoryLabelKey: "test-repo",
			},
		},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingPR).Build()
	validator := NewPackageRevisionValidator(client)

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
		},
	}

	err := validator.validateWorkspaceUniqueness(context.Background(), newPR)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace")
}

// TestUnmarshalInto tests the unmarshalInto helper method.
func TestUnmarshalInto(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	tests := []struct {
		name      string
		raw       []byte
		target    interface{}
		fieldName string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid unmarshaling",
			raw:       []byte(`{"apiVersion":"porch.kpt.dev/v1alpha2","kind":"PackageRevision"}`),
			target:    &v1alpha2.PackageRevision{},
			fieldName: "test",
			wantErr:   false,
		},
		{
			name:      "empty bytes",
			raw:       []byte{},
			target:    &v1alpha2.PackageRevision{},
			fieldName: "test",
			wantErr:   true,
			errMsg:    "is empty",
		},
		{
			name:      "malformed JSON",
			raw:       []byte(`{invalid}`),
			target:    &v1alpha2.PackageRevision{},
			fieldName: "test",
			wantErr:   true,
			errMsg:    "failed to unmarshal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := validator.unmarshalInto(tt.raw, tt.target, tt.fieldName)
			if tt.wantErr {
				require.NotNil(t, resp)
				assert.Contains(t, resp.Result.Message, tt.errMsg)
			} else {
				assert.Nil(t, resp)
			}
		})
	}
}

// TestUnmarshalPackageRevision tests the unmarshalPackageRevision helper method.
func TestUnmarshalPackageRevision(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	tests := []struct {
		name      string
		raw       []byte
		fieldName string
		wantErr   bool
	}{
		{
			name:      "valid object",
			raw:       []byte(`{"apiVersion":"porch.kpt.dev/v1alpha2","kind":"PackageRevision","metadata":{"name":"test"}}`),
			fieldName: "test",
			wantErr:   false,
		},
		{
			name:      "empty bytes",
			raw:       []byte{},
			fieldName: "test",
			wantErr:   true,
		},
		{
			name:      "malformed JSON",
			raw:       []byte(`{invalid}`),
			fieldName: "test",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, errResp := validator.unmarshalPackageRevision(tt.raw, tt.fieldName)
			if tt.wantErr {
				require.NotNil(t, errResp)
				assert.Nil(t, pr)
			} else {
				assert.Nil(t, errResp)
				require.NotNil(t, pr)
			}
		})
	}
}

// TestHandleCreateValid tests handleCreate with valid object.
func TestHandleCreateValid(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	rawBytes, _ := json.Marshal(pr)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.handleCreate(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleCreateValidationFails tests handleCreate when validation fails.
func TestHandleCreateValidationFails(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	validator := NewPackageRevisionValidator(client)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "nonexistent",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	rawBytes, _ := json.Marshal(pr)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.handleCreate(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "not found")
}

// TestHandleUpdateValid tests handleUpdate with valid objects and transition.
func TestHandleUpdateValid(t *testing.T) {
	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	oldBytes, _ := json.Marshal(oldPR)
	newBytes, _ := json.Marshal(newPR)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: oldBytes},
			Object:    runtime.RawExtension{Raw: newBytes},
		},
	}

	resp := validator.handleUpdate(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleUpdateImmutableFieldFails tests handleUpdate when immutable field changes.
func TestHandleUpdateImmutableFieldFails(t *testing.T) {
	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "different-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	oldBytes, _ := json.Marshal(oldPR)
	newBytes, _ := json.Marshal(newPR)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: oldBytes},
			Object:    runtime.RawExtension{Raw: newBytes},
		},
	}

	resp := validator.handleUpdate(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "immutable")
}

// TestHandleDeleteValid tests handleDelete with valid object.
func TestHandleDeleteValid(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default", UID: "uid-123"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
		},
	}
	rawBytes, _ := json.Marshal(pr)

	validator := NewPackageRevisionValidator(fake.NewClientBuilder().WithScheme(scheme).Build())

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			OldObject: runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.handleDelete(context.Background(), req)
	assert.True(t, resp.Allowed)
}

// TestHandleDeleteWithReference tests handleDelete rejects when referenced.
func TestHandleDeleteWithReference(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)

	prToDelete := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "upstream-pr", Namespace: "default", UID: "uid-upstream"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "upstream",
			WorkspaceName:  "ws",
		},
	}

	dependentPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream-pr", Namespace: "default", UID: "uid-downstream"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "downstream",
			WorkspaceName:  "ws",
			Source: &v1alpha2.PackageSource{
				CopyFrom: &v1alpha2.PackageRevisionRef{Name: "upstream-pr"},
			},
		},
	}

	rawBytes, _ := json.Marshal(prToDelete)

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dependentPR).Build()
	validator := NewPackageRevisionValidator(client)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Delete,
			OldObject: runtime.RawExtension{Raw: rawBytes},
		},
	}

	resp := validator.handleDelete(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "referenced")
}

// TestValidateCreateWithInvalidSourceReference tests that CREATE succeeds even with
// non-existent source references (source validation is deferred to controller).
func TestValidateCreateWithInvalidSourceReference(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
			Source: &v1alpha2.PackageSource{
				CopyFrom: &v1alpha2.PackageRevisionRef{Name: "nonexistent-upstream"},
			},
		},
	}

	// Webhook allows CREATE (source validation is in controller, not webhook)
	_, err := validator.ValidateCreate(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateUpdateBlocksProposedDuringRender prevents propose when render in progress.
func TestValidateUpdateBlocksProposedDuringRender(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)

	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecycleProposed,
		},
		Status: v1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "v1", // Render in progress
		},
	}

	_, err := validator.ValidateUpdate(context.Background(), oldPR, newPR)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render race prevention")
}

// TestValidateUpdateBlocksPublishWhenVersionsMismatch blocks publish when render not observed.
func TestValidateUpdateBlocksPublishWhenVersionsMismatch(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)

	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Annotations: map[string]string{
				v1alpha2.AnnotationRenderRequest: "v2",
			},
		},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecyclePublished,
		},
		Status: v1alpha2.PackageRevisionStatus{
			ObservedPrrResourceVersion: "v1", // Versions don't match - render not observed
		},
	}

	_, err := validator.ValidateUpdate(context.Background(), oldPR, newPR)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render race prevention")
}

// TestValidateUpdateAllowsPublishWhenNoRenderInProgress allows publish when render not in progress.
func TestValidateUpdateAllowsPublishWhenNoRenderInProgress(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)

	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Annotations: map[string]string{
				v1alpha2.AnnotationRenderRequest: "54321",
			},
		},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecyclePublished,
		},
		Status: v1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "",      // No render in progress
			ObservedPrrResourceVersion:  "54321", // Matches annotation - render observed
		},
	}

	_, err := validator.ValidateUpdate(context.Background(), oldPR, newPR)
	assert.NoError(t, err)
}

// TestValidateUpdateSkipsRenderCheckForDraftTransitions skips render guard for draft.
func TestValidateUpdateSkipsRenderCheckForDraftTransitions(t *testing.T) {
	validator := NewPackageRevisionValidator(nil)

	oldPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	newPR := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			Lifecycle: v1alpha2.PackageRevisionLifecycleDraft,
		},
		Status: v1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "v1", // Even with render in progress
		},
	}

	_, err := validator.ValidateUpdate(context.Background(), oldPR, newPR)
	assert.NoError(t, err)
}

// TestValidateDeleteAllowsAllOnDeletionTimestamp tests ValidateDelete allows all deletes when marked for GC.
func TestValidateDeleteAllowsAllOnDeletionTimestamp(t *testing.T) {
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().Build())

	now := metav1.Now()

	lifecycles := []v1alpha2.PackageRevisionLifecycle{
		v1alpha2.PackageRevisionLifecycleDraft,
		v1alpha2.PackageRevisionLifecycleProposed,
		v1alpha2.PackageRevisionLifecyclePublished,
		v1alpha2.PackageRevisionLifecycleDeletionProposed,
	}

	for _, lc := range lifecycles {
		t.Run(string(lc), func(t *testing.T) {
			pr := &v1alpha2.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-pr",
					Namespace:         "default",
					DeletionTimestamp: &now,
				},
				Spec: v1alpha2.PackageRevisionSpec{
					RepositoryName: "test-repo",
					PackageName:    "pkg",
					WorkspaceName:  "ws",
					Lifecycle:      lc,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), pr)
			assert.NoError(t, err, "should allow deletion of %s when deletionTimestamp is set", lc)
		})
	}
}

// TestValidateDeleteDeletionProposedPackage tests ValidateDelete allows deletion of DeletionProposed packages.
func TestValidateDeleteDeletionProposedPackage(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().WithScheme(scheme).Build())

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDeletionProposed,
		},
	}

	_, err := validator.ValidateDelete(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateDeleteDraftPackage tests ValidateDelete allows deletion of Draft packages.
func TestValidateDeleteDraftPackage(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	validator := NewPackageRevisionValidator(fake.NewClientBuilder().WithScheme(scheme).Build())

	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	_, err := validator.ValidateDelete(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateDeletePublishedPackageManualDelete tests ValidateDelete blocks manual deletion of Published packages.
// Published packages must transition to DeletionProposed first (lifecycle protection).
func TestValidateDeletePublishedPackageManualDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	// Published package without DeletionTimestamp - direct delete should be rejected
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	_, err := validator.ValidateDelete(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DeletionProposed")
}

// TestValidateDeletePublishedPackageCascadeDelete tests ValidateDelete allows cascade deletion of Published packages.
// When the owning Repository is being deleted, lifecycle protection is bypassed.
func TestValidateDeletePublishedPackageCascadeDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	// Repository IS being deleted (has DeletionTimestamp and finalizer for fake client validation)
	now := metav1.Now()
	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-repo",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test.finalizer"}, // Required for fake client with DeletionTimestamp
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()
	validator := NewPackageRevisionValidator(client)

	// Published package — should be allowed because the owner repo is being deleted (cascade)
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "default",
		},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	_, err := validator.ValidateDelete(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateDeletePublishedPackageRepoGone tests ValidateDelete allows deletion when the owner repo is already gone.
func TestValidateDeletePublishedPackageRepoGone(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha2.AddToScheme(scheme)
	configapi.AddToScheme(scheme)

	// No repo object in the fake client — simulates repo already deleted
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	validator := NewPackageRevisionValidator(client)

	// Published package whose repo no longer exists — should be allowed (orphan cleanup)
	pr := &v1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "default",
		},
		Spec: v1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "pkg",
			WorkspaceName:  "ws",
			Lifecycle:      v1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	_, err := validator.ValidateDelete(context.Background(), pr)
	assert.NoError(t, err)
}
