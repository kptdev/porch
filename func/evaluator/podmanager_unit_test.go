// Copyright 2025 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package evaluator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/kptdev/porch/func/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPodID(t *testing.T) {
	tests := []struct {
		name        string
		image       string
		hash        string
		postFix     string
		expected    string
		expectError bool
	}{
		{
			name:     "standard image with tag",
			image:    "ghcr.io/kptdev/krm-functions-catalog/apply-replacements:latest",
			hash:     "5245a52778d684fa698f69861fb2e058b308f6a74fed5bf2fe77d97bad5e071c",
			postFix:  "1",
			expected: "apply-replacements-latest-1-5245a527",
		},
		{
			name:     "image without explicit tag defaults to latest",
			image:    "ghcr.io/kptdev/krm-functions-catalog/set-namespace",
			hash:     "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			postFix:  "2",
			expected: "set-namespace-latest-2-abcdef12",
		},
		{
			name:     "image with version tag containing dots",
			image:    "ghcr.io/kptdev/krm-functions-catalog/set-labels:v0.4.1",
			hash:     "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			postFix:  "1",
			expected: "set-labels-v041-1-12345678",
		},
		{
			name:     "image with underscores in name",
			image:    "ghcr.io/kptdev/krm-functions-catalog/my_function:latest",
			hash:     "aabbccddee112233aabbccddee112233aabbccddee112233aabbccddee112233",
			postFix:  "1",
			expected: "my-function-latest-1-aabbccdd",
		},
		{
			name:        "invalid image reference",
			image:       "invalid@oci@ref",
			hash:        "deadbeef",
			postFix:     "1",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := podID(tt.image, tt.hash, tt.postFix)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestIsPodTemplateSameVersion(t *testing.T) {
	tests := []struct {
		name            string
		pod             *corev1.Pod
		templateVersion string
		expected        bool
	}{
		{
			name: "matching version",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						templateVersionAnnotation: "v1",
					},
				},
			},
			templateVersion: "v1",
			expected:        true,
		},
		{
			name: "mismatching version",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						templateVersionAnnotation: "v1",
					},
				},
			},
			templateVersion: "v2",
			expected:        false,
		},
		{
			name: "no annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			templateVersion: "v1",
			expected:        false,
		},
		{
			name: "nil annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			templateVersion: "v1",
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPodTemplateSameVersion(tt.pod, tt.templateVersion)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasImagePullSecret(t *testing.T) {
	tests := []struct {
		name            string
		podTemplateSpec *corev1.PodTemplateSpec
		secretName      string
		expected        bool
	}{
		{
			name: "secret found",
			podTemplateSpec: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{
						{Name: "my-secret"},
						{Name: "other-secret"},
					},
				},
			},
			secretName: "my-secret",
			expected:   true,
		},
		{
			name: "secret not found",
			podTemplateSpec: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{
						{Name: "other-secret"},
					},
				},
			},
			secretName: "my-secret",
			expected:   false,
		},
		{
			name: "empty secrets list",
			podTemplateSpec: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{},
				},
			},
			secretName: "my-secret",
			expected:   false,
		},
		{
			name: "nil secrets list",
			podTemplateSpec: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
			secretName: "my-secret",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasImagePullSecret(tt.podTemplateSpec, tt.secretName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPatchNewPodContainer(t *testing.T) {
	pm := &podManager{maxGrpcMessageSize: 4 * 1024 * 1024}

	t.Run("patches function container successfully", func(t *testing.T) {
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    functionContainerName,
						Image:   "to-be-replaced",
						Command: []string{filepath.Join(volumeMountPath, wrapperServerBin)},
					},
				},
			},
		}
		de := DigestAndEntrypoint{
			Digest:     "abc123",
			Entrypoint: []string{"/my-function"},
		}

		err := pm.patchNewPodContainer(podTemplateSpec, de, "ghcr.io/kptdev/krm-functions-catalog/my-function:latest")
		require.NoError(t, err)

		container := podTemplateSpec.Spec.Containers[0]
		assert.Equal(t, "ghcr.io/kptdev/krm-functions-catalog/my-function:latest", container.Image)
		assert.Contains(t, container.Args, "--port")
		assert.Contains(t, container.Args, defaultWrapperServerPort)
		assert.Contains(t, container.Args, "--")
		assert.Contains(t, container.Args, "/my-function")
	})

	t.Run("returns error when function container not found", func(t *testing.T) {
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "not-a-function",
						Image: "some-image",
					},
				},
			},
		}
		de := DigestAndEntrypoint{
			Digest:     "abc123",
			Entrypoint: []string{"/entry"},
		}

		err := pm.patchNewPodContainer(podTemplateSpec, de, "test-image")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), functionContainerName)
	})

	t.Run("handles multiple containers", func(t *testing.T) {
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "sidecar", Image: "sidecar-image"},
					{
						Name:    functionContainerName,
						Image:   "to-be-replaced",
						Command: []string{filepath.Join(volumeMountPath, wrapperServerBin)},
					},
				},
			},
		}
		de := DigestAndEntrypoint{
			Digest:     "abc123",
			Entrypoint: []string{"/fn"},
		}

		err := pm.patchNewPodContainer(podTemplateSpec, de, "my-image")
		require.NoError(t, err)
		assert.Equal(t, "sidecar-image", podTemplateSpec.Spec.Containers[0].Image, "sidecar should be unchanged")
		assert.Equal(t, "my-image", podTemplateSpec.Spec.Containers[1].Image, "function container should be patched")
	})
}

func TestPatchNewPodMetadata(t *testing.T) {
	pm := &podManager{namespace: "test-ns"}

	t.Run("sets namespace labels and annotations from scratch", func(t *testing.T) {
		podTemplateSpec := &corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{},
		}
		pm.patchNewPodMetadata(podTemplateSpec, "my-pod-id", "v2")

		assert.Equal(t, "test-ns", podTemplateSpec.Namespace)
		assert.Equal(t, "v2", podTemplateSpec.Annotations[templateVersionAnnotation])
		assert.Equal(t, "my-pod-id", podTemplateSpec.Labels[krmFunctionImageLabel])
	})

	t.Run("preserves existing annotations and labels", func(t *testing.T) {
		podTemplateSpec := &corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{"existing-key": "existing-val"},
				Labels:      map[string]string{"existing-label": "label-val"},
			},
		}
		pm.patchNewPodMetadata(podTemplateSpec, "pod-id", "v1")

		assert.Equal(t, "existing-val", podTemplateSpec.Annotations["existing-key"], "existing annotations preserved")
		assert.Equal(t, "v1", podTemplateSpec.Annotations[templateVersionAnnotation], "template version set")
		assert.Equal(t, "label-val", podTemplateSpec.Labels["existing-label"], "existing labels preserved")
		assert.Equal(t, "pod-id", podTemplateSpec.Labels[krmFunctionImageLabel], "function image label set")
	})
}

func TestAppendImagePullSecret(t *testing.T) {
	t.Run("appends secret for private registry image", func(t *testing.T) {
		pm := &podManager{
			enablePrivateRegistries: true,
			registryAuthSecretName:  "my-auth-secret",
		}
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{},
		}
		pm.appendImagePullSecret("private.registry.io/my-fn:latest", podTemplateSpec)
		require.Len(t, podTemplateSpec.Spec.ImagePullSecrets, 1)
		assert.Equal(t, "my-auth-secret", podTemplateSpec.Spec.ImagePullSecrets[0].Name)
	})

	t.Run("does not append if secret already exists", func(t *testing.T) {
		pm := &podManager{
			enablePrivateRegistries: true,
			registryAuthSecretName:  "my-auth-secret",
		}
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "my-auth-secret"},
				},
			},
		}
		pm.appendImagePullSecret("private.registry.io/my-fn:latest", podTemplateSpec)
		assert.Len(t, podTemplateSpec.Spec.ImagePullSecrets, 1, "should not duplicate")
	})

	t.Run("does not append for default registry", func(t *testing.T) {
		pm := &podManager{
			enablePrivateRegistries: true,
			registryAuthSecretName:  "my-auth-secret",
		}
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{},
		}
		pm.appendImagePullSecret(defaultRegistry+"my-fn:latest", podTemplateSpec)
		assert.Empty(t, podTemplateSpec.Spec.ImagePullSecrets)
	})

	t.Run("does not append when private registries disabled", func(t *testing.T) {
		pm := &podManager{
			enablePrivateRegistries: false,
			registryAuthSecretName:  "my-auth-secret",
		}
		podTemplateSpec := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{},
		}
		pm.appendImagePullSecret("private.registry.io/my-fn:latest", podTemplateSpec)
		assert.Empty(t, podTemplateSpec.Spec.ImagePullSecrets)
	})
}

func TestLoadTLSConfig(t *testing.T) {
	t.Run("valid PEM certificate", func(t *testing.T) {
		// Generate a self-signed certificate for testing
		certPEM := generateSelfSignedCertPEM(t)

		tmpFile, err := os.CreateTemp("", "ca-cert-*.pem")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.Write(certPEM)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		tlsConfig, err := loadTLSConfig(tmpFile.Name())
		require.NoError(t, err)
		assert.NotNil(t, tlsConfig)
		assert.NotNil(t, tlsConfig.RootCAs)
	})

	t.Run("invalid PEM data", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "ca-cert-invalid-*.pem")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString("not a valid PEM certificate")
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		_, err = loadTLSConfig(tmpFile.Name())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to append certificates")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := loadTLSConfig("/nonexistent/ca.pem")
		assert.Error(t, err)
	})
}

func TestCreateTransport(t *testing.T) {
	certPEM := generateSelfSignedCertPEM(t)

	tmpFile, err := os.CreateTemp("", "transport-cert-*.pem")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.Write(certPEM)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	tlsConfig, err := loadTLSConfig(tmpFile.Name())
	require.NoError(t, err)

	transport := createTransport(tlsConfig)
	assert.NotNil(t, transport)
	assert.Equal(t, tlsConfig, transport.TLSClientConfig)
}

func TestFindPodsForService(t *testing.T) {
	t.Run("returns matching pods", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "my-fn"},
			},
		}
		matchingPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "matching-pod",
				Namespace: "test-ns",
				Labels:    map[string]string{"app": "my-fn"},
			},
		}
		nonMatchingPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-pod",
				Namespace: "test-ns",
				Labels:    map[string]string{"app": "other"},
			},
		}

		kubeClient := fake.NewClientBuilder().WithObjects(matchingPod, nonMatchingPod).Build()
		pm := &podManager{kubeClient: kubeClient}

		pods, err := pm.findPodsForService(t.Context(), svc)
		require.NoError(t, err)
		assert.Len(t, pods, 1)
		assert.Equal(t, "matching-pod", pods[0].Name)
	})

	t.Run("returns empty when no pods match", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "no-match"},
			},
		}

		kubeClient := fake.NewClientBuilder().Build()
		pm := &podManager{kubeClient: kubeClient}

		pods, err := pm.findPodsForService(t.Context(), svc)
		require.NoError(t, err)
		assert.Empty(t, pods)
	})
}

func TestInspectOrCreateSecret(t *testing.T) {
	t.Run("creates secret when not exists", func(t *testing.T) {
		// Write a temporary docker config file
		tmpFile, err := os.CreateTemp("", "dockerconfig-*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		dockerConfig := `{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`
		_, err = tmpFile.WriteString(dockerConfig)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		kubeClient := fake.NewClientBuilder().Build()
		pm := &podManager{
			kubeClient: kubeClient,
			namespace:  "test-ns",
		}

		err = pm.InspectOrCreateSecret(t.Context(), tmpFile.Name(), "test-secret")
		require.NoError(t, err)

		// Verify secret was created
		var secret corev1.Secret
		err = kubeClient.Get(t.Context(), client.ObjectKey{Name: "test-secret", Namespace: "test-ns"}, &secret)
		require.NoError(t, err)
		assert.Equal(t, corev1.SecretTypeDockerConfigJson, secret.Type)
		assert.Equal(t, dockerConfig, string(secret.Data[".dockerconfigjson"]))
	})

	t.Run("does not recreate when secret matches", func(t *testing.T) {
		dockerConfig := `{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`

		tmpFile, err := os.CreateTemp("", "dockerconfig-*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		_, err = tmpFile.WriteString(dockerConfig)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-secret", Namespace: "test-ns"},
			Data:       map[string][]byte{".dockerconfigjson": []byte(dockerConfig)},
			Type:       corev1.SecretTypeDockerConfigJson,
		}

		kubeClient := fake.NewClientBuilder().WithObjects(existingSecret).Build()
		pm := &podManager{
			kubeClient: kubeClient,
			namespace:  "test-ns",
		}

		err = pm.InspectOrCreateSecret(t.Context(), tmpFile.Name(), "test-secret")
		require.NoError(t, err)
	})

	t.Run("updates secret when content differs", func(t *testing.T) {
		newDockerConfig := `{"auths":{"new-registry.example.com":{"auth":"bmV3OnRlc3Q="}}}`

		tmpFile, err := os.CreateTemp("", "dockerconfig-*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		_, err = tmpFile.WriteString(newDockerConfig)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-secret", Namespace: "test-ns"},
			Data:       map[string][]byte{".dockerconfigjson": []byte(`{"auths":{"old-registry.example.com":{}}}`)},
			Type:       corev1.SecretTypeDockerConfigJson,
		}

		kubeClient := fake.NewClientBuilder().WithObjects(existingSecret).Build()
		pm := &podManager{
			kubeClient: kubeClient,
			namespace:  "test-ns",
		}

		err = pm.InspectOrCreateSecret(t.Context(), tmpFile.Name(), "test-secret")
		require.NoError(t, err)

		// Verify secret was updated
		var secret corev1.Secret
		err = kubeClient.Get(t.Context(), client.ObjectKey{Name: "test-secret", Namespace: "test-ns"}, &secret)
		require.NoError(t, err)
		assert.Equal(t, newDockerConfig, string(secret.Data[".dockerconfigjson"]))
	})

	t.Run("returns error for missing auth file", func(t *testing.T) {
		kubeClient := fake.NewClientBuilder().Build()
		pm := &podManager{
			kubeClient: kubeClient,
			namespace:  "test-ns",
		}

		err := pm.InspectOrCreateSecret(t.Context(), "/nonexistent/path", "test-secret")
		assert.Error(t, err)
	})
}

func TestGetCustomAuth(t *testing.T) {
	t.Run("parses docker config and returns authenticator", func(t *testing.T) {
		dockerConfig := `{"auths":{"gcr.io":{"username":"_json_key","password":"secret","auth":"X2pzb25fa2V5OnNlY3JldA=="}}}`

		tmpFile, err := os.CreateTemp("", "dockerconfig-*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		_, err = tmpFile.WriteString(dockerConfig)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		pm := &podManager{}
		// Use a reference that resolves to gcr.io
		ref, err := parseTestReference("gcr.io/my-project/my-image:latest")
		require.NoError(t, err)

		auth, err := pm.getCustomAuth(ref, tmpFile.Name())
		require.NoError(t, err)
		assert.NotNil(t, auth)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		pm := &podManager{}
		ref, err := parseTestReference("gcr.io/my-project/my-image:latest")
		require.NoError(t, err)

		_, err = pm.getCustomAuth(ref, "/nonexistent/path")
		assert.Error(t, err)
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "dockerconfig-*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		_, err = tmpFile.WriteString("{invalid json")
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		pm := &podManager{}
		ref, err := parseTestReference("gcr.io/my-project/my-image:latest")
		require.NoError(t, err)

		_, err = pm.getCustomAuth(ref, tmpFile.Name())
		assert.Error(t, err)
	})
}

// generateSelfSignedCertPEM generates a self-signed certificate PEM for testing.
func generateSelfSignedCertPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageCertSign,
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

// parseTestReference is a helper to parse an image reference for testing.
func parseTestReference(image string) (name.Reference, error) {
	return name.ParseReference(image)
}
