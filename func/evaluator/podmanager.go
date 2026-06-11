// Copyright 2022, 2026 The kpt Authors
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

package evaluator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/kptdev/kpt/pkg/fn/runtime"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/kptdev/porch/func/types"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	basePodTemplateName     = "base-pod-template"
	baseServiceTemplateName = "base-service-template"
)

var (
	defaultWrapperServerPortI32 = func() int32 {
		asInt64, _ := strconv.ParseInt(defaultWrapperServerPort, 10, 32)
		return int32(asInt64)
	}()
	inlineBasePodTemplate = &corev1.PodTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PodTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: basePodTemplateName,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"cluster-autoscaler.kubernetes.io/safe-to-evict": "true",
				},
			},
			Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name: "copy-wrapper-server",
						Command: []string{
							"cp",
							"-a",
							"/home/nonroot/wrapper-server/.",
							volumeMountPath,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      volumeName,
								MountPath: volumeMountPath,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:    functionContainerName,
						Image:   "to-be-replaced",
						Command: []string{filepath.Join(volumeMountPath, wrapperServerBin)},
						Env: []corev1.EnvVar{
							{
								Name:  "OTEL_METRICS_EXPORTER",
								Value: "prometheus",
							},
							{
								Name:  "OTEL_TRACES_EXPORTER",
								Value: "none",
							},
							{
								Name:  "OTEL_EXPORTER_PROMETHEUS_HOST",
								Value: "0.0.0.0",
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								// TODO: use the k8s native GRPC prober when it has been rolled out in GKE.
								// https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/#define-a-grpc-liveness-probe
								Exec: &corev1.ExecAction{
									Command: []string{
										filepath.Join(volumeMountPath, gRPCProbeBin),
										"-addr", net.JoinHostPort("localhost", fmt.Sprint(defaultWrapperServerPort)),
									},
								},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      volumeName,
								MountPath: volumeMountPath,
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: volumeName,
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
		},
	}
	inlineBaseServiceTemplate = &configapi.ServiceTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configapi.GroupVersion.String(),
			Kind:       "ServiceTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: baseServiceTemplateName,
		},
		Template: configapi.ServiceTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"internal.kpt.dev/upstream-identifier": "|Service|porch-system|function-runner",
				},
				Labels: map[string]string{
					"app.kubernetes.io/name": "to-be-replaced",
				},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					krmFunctionImageLabel: "to-be-replaced",
				},
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       defaultWrapperServerPortI32,
						TargetPort: intstr.FromInt32(defaultWrapperServerPortI32),
					},
				},
				Type: corev1.ServiceTypeClusterIP,
			},
		},
	}
)

// podManager is responsible for:
// - creating a pod
// - waiting for the pod to be running and ready
// - caching the metadata (e.g. entrypoint) for the image.
type podManager struct {
	// kubeClient is the kubernetes client
	kubeClient client.Client
	// namespace holds the namespace where the executors run
	namespace string

	//Namespace where the function-runner is running
	managerNamespace string

	// wrapperServerImage is the image name of the wrapper server
	wrapperServerImage string

	// podReadyCh is a channel to receive requests to get GRPC client from each function evaluation request handler.
	podReadyCh chan<- *PodReadyResponse

	// imageMetadataCache is a cache of image name to DigestAndEntrypoint.
	// Only podManager is allowed to touch this cache.
	// Its underlying type is map[string]*DigestAndEntrypoint.
	imageMetadataCache sync.Map

	// podReadyTimeout is the timeout podManager will wait for the pod to be ready before reporting an error
	podReadyTimeout time.Duration

	// The maximum size of grpc messages sent to KRM function evaluator pods
	maxGrpcMessageSize int

	// If true enables the use of private registries and their authentication
	enablePrivateRegistries bool
	// The path of the secret used for authenticating to custom registries
	registryAuthSecretPath string
	// The name of the secret used for authenticating to custom registries
	registryAuthSecretName string
	// If enabled, will prioritize use of user provided TLS secret when accessing registries
	enablePrivateRegistriesTls bool
	// The path of the secret used in tls configuration
	tlsSecretPath string
	// Image resolver that prepends a prefix if necessary
	imageResolver runneroptions.ImageResolveFunc
	// tagResolver is used to resolve the tag of the given image
	tagResolver runtime.TagResolver
}

func (pm *podManager) createPodData(ctx context.Context, serviceKey client.ObjectKey, podKey client.ObjectKey, image string) (*PodData, error) {
	podData := &PodData{
		Image:      image,
		PodKey:     &podKey,
		ServiceKey: &serviceKey,
	}
	serviceUrl, err := pm.getServiceUrlOnceEndpointActive(ctx, serviceKey, podKey)
	if err != nil {
		return podData, err
	}
	if serviceUrl == "" {
		return podData, fmt.Errorf("service %s/%s did not have serviceIP", serviceKey.Namespace, serviceKey.Name)
	}
	address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
	cc, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(pm.maxGrpcMessageSize),
			grpc.MaxCallSendMsgSize(pm.maxGrpcMessageSize),
			grpc.WaitForReady(true),
		),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return podData, fmt.Errorf("failed to dial grpc function evaluator on %q for pod %s/%s: %w", address, serviceKey.Namespace, serviceKey.Name, err)
	}
	podData.GrpcConnection = cc
	return podData, err
}

// getFuncEvalPodClient ensures there is a pod running and ready for the image.
// It will send it to the podReadyCh channel when the pod is ready. ttl is the
// time-to-live period for the pod. If useGenerateName is false, it will try to
// create a pod with a fixed name. Otherwise, it will create a pod and let the
// apiserver to generate the name from a template.
func (pm *podManager) getFuncEvalPodClient(ctx context.Context, image string, postFix int, podConfig *configapi.PodExecutorConfig, useGenerateName bool) {
	c, err := func() (*PodData, error) {
		podData := &PodData{
			Image: image,
		}
		pod, err := pm.CreatePod(ctx, image, postFix, podConfig, useGenerateName)
		if err != nil {

			return podData, err
		}

		podKey := client.ObjectKeyFromObject(pod)
		podData.PodKey = &podKey

		// Service name is Image Label set on Pod manifest
		serviceName := pod.Labels[krmFunctionImageLabel]

		serviceTemplate, err := pm.retrieveOrCreateService(ctx, serviceName)
		if err != nil {
			return podData, err
		}
		serviceKey := client.ObjectKeyFromObject(serviceTemplate)
		podData, err = pm.createPodData(ctx, serviceKey, podKey, image)
		return podData, err
	}()

	pm.podReadyCh <- &PodReadyResponse{
		PodData: *c,
		Err:     err,
	}
}

func (pm *podManager) InspectOrCreateSecret(ctx context.Context, registryAuthSecretPath string, registryAuthSecretName string) error {
	podSecret := &corev1.Secret{}
	// using pod manager client since this secret is only related to these pods and nothing else
	err := pm.kubeClient.Get(context.Background(), client.ObjectKey{
		Name:      registryAuthSecretName,
		Namespace: pm.namespace,
	}, podSecret)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Error other than "not found" occurred
			return err
		}
		klog.Infof("Secret for private registry pods does not exist and is required. Generating Secret Now")
		dockerConfigBytes, err := os.ReadFile(registryAuthSecretPath)
		if err != nil {
			return err
		}
		// Secret does not exist, create it
		podSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      registryAuthSecretName,
				Namespace: pm.namespace,
			},
			Data: map[string][]byte{
				".dockerconfigjson": dockerConfigBytes,
			},
			Type: corev1.SecretTypeDockerConfigJson,
		}
		err = pm.kubeClient.Create(ctx, podSecret)
		if err != nil {
			return err
		}

		klog.Infof("Private registry secret created successfully")
	} else {
		klog.Infof("Private registry secret already exists")
		// use the bytes Data of the user secret and compare it to the data of the pod secret
		dockerConfigBytes, err := os.ReadFile(registryAuthSecretPath)
		if err != nil {
			return err
		}
		// Compare the data of the two secrets
		if string(podSecret.Data[".dockerconfigjson"]) == string(dockerConfigBytes) {
			klog.Infof("The data content of the user given secret matches the private registry secret.")
		} else {
			klog.Infof("The data content of the private registry secret does not match given secret")
			// Patch the secret on the pods with the data from the user secret
			podSecret.Data[".dockerconfigjson"] = dockerConfigBytes
			err = pm.kubeClient.Update(ctx, podSecret)
			if err != nil {
				return err
			}
			klog.Infof("Private registry secret patched successfully.")
		}
	}
	return nil
}

// imageDigestAndEntrypoint gets the entrypoint of a container image by looking at its metadata.
func (pm *podManager) imageDigestAndEntrypoint(ctx context.Context, image string) (*DigestAndEntrypoint, error) {
	val, found := pm.imageMetadataCache.Load(image)
	if found {
		return val.(*DigestAndEntrypoint), nil
	}

	start := time.Now()
	defer func() {
		klog.Infof("getting image metadata for %v took %v", image, time.Since(start))
	}()

	ref, err := name.ParseReference(image)
	if err != nil {
		klog.Errorf("we got an error parsing the ref %v", err)
		return nil, err
	}

	var auth authn.Authenticator
	if pm.enablePrivateRegistries && !strings.HasPrefix(image, defaultRegistry) {
		if err := pm.ensureCustomAuthSecret(ctx, pm.registryAuthSecretPath, pm.registryAuthSecretName); err != nil {
			return nil, err
		}

		auth, err = pm.getCustomAuth(ref, pm.registryAuthSecretPath)
		if err != nil {
			return nil, err
		}
	} else {
		auth, err = authn.DefaultKeychain.Resolve(ref.Context())
		if err != nil {
			klog.Errorf("error resolving default keychain: %v", err)
			return nil, err
		}
	}

	return pm.getImageMetadata(ctx, ref, auth, image)
}

// ensureCustomAuthSecret ensures that, if an image from a custom registry is requested, the appropriate credentials are passed into a secret for function pods to use when pulling. If the secret does not already exist, it is created.
func (pm *podManager) ensureCustomAuthSecret(ctx context.Context, registryAuthSecretPath string, registryAuthSecretName string) error {
	if err := pm.InspectOrCreateSecret(ctx, registryAuthSecretPath, registryAuthSecretName); err != nil {
		return err
	}
	return nil
}

// getCustomAuth reads and parses the custom registry auth file from the mounted secret.
func (pm *podManager) getCustomAuth(ref name.Reference, registryAuthSecretPath string) (authn.Authenticator, error) {
	dockerConfigBytes, err := os.ReadFile(registryAuthSecretPath)
	if err != nil {
		klog.Errorf("error reading authentication file %v", err)
		return nil, err
	}

	var dockerConfig DockerConfig
	if err := json.Unmarshal(dockerConfigBytes, &dockerConfig); err != nil {
		klog.Errorf("error unmarshaling authentication file %v", err)
		return nil, err
	}

	return authn.FromConfig(dockerConfig.Auths[ref.Context().RegistryStr()]), nil
}

// getImageMetadata retrieves the image digest and entrypoint.
func (pm *podManager) getImageMetadata(ctx context.Context, ref name.Reference, auth authn.Authenticator, image string) (*DigestAndEntrypoint, error) {
	img, err := pm.getImage(ctx, ref, auth, image)
	if err != nil {
		return nil, err
	}
	hash, err := img.Digest()
	if err != nil {
		return nil, err
	}
	configFile, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	// TODO: to handle all scenario, we should follow https://docs.docker.com/engine/reference/builder/#understand-how-cmd-and-entrypoint-interact.
	cfg := configFile.Config
	entrypoint := cfg.Entrypoint
	if len(entrypoint) == 0 {
		entrypoint = cfg.Cmd
	}
	de := &DigestAndEntrypoint{
		Digest:     hash.Hex,
		Entrypoint: entrypoint,
	}
	pm.imageMetadataCache.Store(image, de)
	return de, nil
}

func (pm *podManager) getImage(ctx context.Context, ref name.Reference, auth authn.Authenticator, image string) (containerregistry.Image, error) {
	// if private registries or their appropriate tls configuration are disabled in the config we pull image with default operation otherwise try and use their tls cert's
	if !pm.enablePrivateRegistries || strings.HasPrefix(image, defaultRegistry) || !pm.enablePrivateRegistriesTls {
		return remote.Image(ref, remote.WithAuth(auth), remote.WithContext(ctx))
	}
	tlsFile := "ca.crt"
	// Check if mounted secret location contains CA file.
	if _, err := os.Stat(pm.tlsSecretPath); os.IsNotExist(err) {
		return nil, err
	}
	if _, errCRT := os.Stat(filepath.Join(pm.tlsSecretPath, "ca.crt")); os.IsNotExist(errCRT) {
		if _, errPEM := os.Stat(filepath.Join(pm.tlsSecretPath, "ca.pem")); os.IsNotExist(errPEM) {
			return nil, fmt.Errorf("ca.crt not found: %v, and ca.pem also not found: %v", errCRT, errPEM)
		}
		tlsFile = "ca.pem"
	}
	// Load the custom TLS configuration
	tlsConfig, err := loadTLSConfig(filepath.Join(pm.tlsSecretPath, tlsFile))
	if err != nil {
		return nil, err
	}
	// Create a custom HTTPS transport
	transport := createTransport(tlsConfig)

	// Attempt image pull with given custom TLS cert
	img, tlsErr := remote.Image(ref, remote.WithAuth(auth), remote.WithContext(ctx), remote.WithTransport(transport))
	if tlsErr != nil {
		// Attempt without given custom TLS cert but with default keychain
		klog.Errorf("Pulling image %s with the provided TLS Cert has failed with error %v", image, tlsErr)
		klog.Infof("Attempting image pull with default keychain instead of provided TLS Cert")
		img, err = remote.Image(ref, remote.WithAuth(auth), remote.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to pull image %s with default keychain: %w\n  (pull was retried after this TLS error: %v)", ref.String(), err, tlsErr)
		}
	}
	return img, nil
}

func loadTLSConfig(caCertPath string) (*tls.Config, error) {
	// Read the CA certificate file
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, err
	}
	// Append the CA certificate to the system pool
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append certificates from PEM")
	}
	// Create a tls.Config with the CA pool
	tlsConfig := &tls.Config{
		RootCAs:    caCertPool,
		MinVersion: tls.VersionTLS12,
	}
	return tlsConfig, nil
}

func createTransport(tlsConfig *tls.Config) *http.Transport {
	return &http.Transport{
		TLSClientConfig: tlsConfig,
	}
}

// CreatePod creates a pod for an image.
func (pm *podManager) CreatePod(ctx context.Context, image string, postFix int, config *configapi.PodExecutorConfig, useGenerateName bool) (*corev1.Pod, error) {
	var de *DigestAndEntrypoint
	var err error
	if pm.imageResolver != nil {
		image = pm.imageResolver(image)
	}
	de, err = pm.imageDigestAndEntrypoint(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("unable to get the entrypoint for %v: %w", image, err)
	}

	podId, err := podID(image, de.Digest, strconv.Itoa(postFix))
	if err != nil {
		return nil, err
	}

	podList := &corev1.PodList{}
	podTemplate, err := pm.getBasePodTemplate(ctx)
	if err != nil {
		klog.Errorf("failed to generate a base pod template: %v", err)
		return nil, fmt.Errorf("failed to generate a base pod template: %w", err)
	}
	podTemplateSpec := &podTemplate.Template
	podTemplateSpec.Spec.InitContainers[0].Image = pm.wrapperServerImage
	pm.appendImagePullSecret(image, podTemplateSpec)

	// Check if there is an existing pod for the image and remove it
	err = pm.kubeClient.List(ctx, podList, client.InNamespace(pm.namespace), client.MatchingLabels(map[string]string{krmFunctionImageLabel: podId}))
	if err != nil {
		klog.Warningf("error when listing pods for %q: %v", image, err)
	}
	if err == nil && len(podList.Items) > 0 {
		for _, pod := range podList.Items {
			if pod.DeletionTimestamp == nil {
				err = pm.kubeClient.Delete(ctx, &pod)
				if err != nil {
					return nil, fmt.Errorf("failed to delete old pod %v/%v: %w", pod.Namespace, pod.Name, err)
				}
			}
		}
	}

	err = pm.patchNewPodContainer(podTemplateSpec, *de, image)
	if err != nil {
		return nil, fmt.Errorf("unable to apply the pod: %w", err)
	}
	pm.patchNewPodMetadata(podTemplateSpec, podId, podTemplate.ResourceVersion) // TODO: should this pass metadata.generation instead?

	if config != nil {
		mergeTemplateOverrides(podTemplateSpec, config.TemplateOverrides)
	}

	if useGenerateName {
		podTemplateSpec.GenerateName = podId + "-"
	} else {
		podTemplateSpec.Name = podId
	}

	pod := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: podTemplateSpec.ObjectMeta,
		Spec:       podTemplateSpec.Spec,
	}

	err = pm.kubeClient.Create(ctx, pod)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return pod, nil
		}
		return nil, fmt.Errorf("unable to apply the pod: %w", err)
	}

	klog.Infof("created KRM function evaluator pod %v/%v for %q", pod.Namespace, pod.Name, image)
	return pod, nil
}

func mergeTemplateOverrides(template *corev1.PodTemplateSpec, overrides *configapi.TemplateOverrides) {
	if overrides == nil {
		return
	}

	if overrides.ServiceAccountName != "" {
		template.Spec.ServiceAccountName = overrides.ServiceAccountName
	}

	if overrides.SecurityContext != nil {
		template.Spec.SecurityContext = overrides.SecurityContext
	}

	mergeContainerOverrides(&template.Spec.InitContainers[0], overrides.InitContainer)
	mergeContainerOverrides(&template.Spec.Containers[0], overrides.Container)
}

func mergeContainerOverrides(container *corev1.Container, overrides *configapi.ContainerOverrides) {
	if overrides == nil {
		return
	}

	if overrides.Resources != nil {
		if overrides.Resources.Requests != nil {
			container.Resources.Requests = overrides.Resources.Requests
		}
		if overrides.Resources.Limits != nil {
			container.Resources.Limits = overrides.Resources.Limits
		}
	}

	if overrides.EnvFrom != nil {
		container.EnvFrom = overrides.EnvFrom
	}

	if overrides.Env != nil {
		container.Env = overrides.Env
	}
}

// Either gets the pod template from configmap, or from an inlined pod template. Also provides the version of the template
func (pm *podManager) getBasePodTemplate(ctx context.Context) (*corev1.PodTemplate, error) {
	baseTemplate := &corev1.PodTemplate{}
	err := pm.kubeClient.Get(ctx, client.ObjectKey{Namespace: pm.namespace, Name: basePodTemplateName}, baseTemplate)
	if err == nil {
		return baseTemplate, nil
	}

	if apierrors.IsNotFound(err) {
		klog.Infof("PodTemplate %q not found, creating...", basePodTemplateName)
		return pm.ensureBasePodTemplate(ctx)
	}

	return nil, err
}

func (pm *podManager) ensureBasePodTemplate(ctx context.Context) (*corev1.PodTemplate, error) {
	template := inlineBasePodTemplate.DeepCopy()
	template.Namespace = pm.namespace
	template.Template.Spec.InitContainers[0].Image = pm.wrapperServerImage

	if err := pm.kubeClient.Create(ctx, template); err != nil {
		return nil, err
	}

	return template, nil
}

// retrieveOrCreateService retrieves or creates a Service pointing to the Function PoD
func (pm *podManager) retrieveOrCreateService(ctx context.Context, serviceName string) (*corev1.Service, error) {
	// Try to retrieve the existing Service, if present
	existingService := &corev1.Service{}
	err := pm.kubeClient.Get(ctx, client.ObjectKey{Namespace: pm.namespace, Name: serviceName}, existingService)

	if err == nil {
		klog.Infof("retrieved function evaluator service %v/%v", existingService.Namespace, existingService.Name)
		return existingService, nil
	}

	// If the service does not exist, generate template and create it
	if apierrors.IsNotFound(err) {
		serviceTemplate, err := pm.getBaseServiceTemplate(ctx)
		if err != nil {
			return nil, err
		}

		serviceTemplateSpec := &serviceTemplate.Template

		serviceTemplateSpec.Namespace = pm.namespace
		serviceTemplateSpec.Name = serviceName
		if serviceTemplateSpec.Labels == nil {
			serviceTemplateSpec.Labels = map[string]string{}
		}
		serviceTemplateSpec.Labels["app.kubernetes.io/name"] = serviceName
		if serviceTemplateSpec.Spec.Selector == nil {
			serviceTemplateSpec.Spec.Selector = map[string]string{}
		}
		// Pod has same Label as Service Name (PodId) for correct mapping
		serviceTemplateSpec.Spec.Selector[krmFunctionImageLabel] = serviceName

		service := &corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: pm.namespace},
			Spec:       serviceTemplateSpec.Spec,
		}

		err = pm.kubeClient.Create(ctx, service, client.FieldOwner(fieldManagerName))
		if err != nil {
			klog.Errorf("failed to create service %v/%v: %v", pm.namespace, serviceName, err)
			// Try to retrieve again
			if apierrors.IsAlreadyExists(err) {
				klog.Infof("service %v/%v already exists - trying to retrieve it", pm.namespace, serviceName)
				err = pm.kubeClient.Get(ctx, client.ObjectKey{Namespace: pm.namespace, Name: serviceName}, existingService)
				if err == nil {
					klog.Infof("retrieved function evaluator service %v/%v", existingService.Namespace, existingService.Name)
					return existingService, nil
				}
			}
			return nil, err
		}

		klog.Infof("created frontend service %v for function evaluator pod", serviceName)
		return service, nil
	}

	// Return other errors
	klog.Errorf("failed to retrieve service %v/%v: %v", pm.namespace, serviceName, err)
	return nil, err
}

func (pm *podManager) getBaseServiceTemplate(ctx context.Context) (*configapi.ServiceTemplate, error) {
	baseTemplate := &configapi.ServiceTemplate{}
	err := pm.kubeClient.Get(ctx, client.ObjectKey{Namespace: pm.namespace, Name: baseServiceTemplateName}, baseTemplate)
	if err == nil {
		return baseTemplate, nil
	}

	if apierrors.IsNotFound(err) {
		klog.Infof("PodTemplate %q not found, creating...", basePodTemplateName)
		return pm.ensureBaseServiceTemplate(ctx)
	}

	return nil, err
}

func (pm *podManager) ensureBaseServiceTemplate(ctx context.Context) (*configapi.ServiceTemplate, error) {
	template := inlineBaseServiceTemplate.DeepCopy()
	template.Namespace = pm.namespace

	if err := pm.kubeClient.Create(ctx, template); err != nil {
		return nil, err
	}

	return template, nil
}

func hasImagePullSecret(podTemplate *corev1.PodTemplateSpec, secretName string) bool {
	for _, secret := range podTemplate.Spec.ImagePullSecrets {
		if secret.Name == secretName {
			return true
		}
	}
	return false
}

// if a custom image is requested, append the secret provided to authenticate to the imagePullSecret of the kpt function pod if it does not already exist
func (pm *podManager) appendImagePullSecret(image string, podTemplate *corev1.PodTemplateSpec) {
	if pm.enablePrivateRegistries && !strings.HasPrefix(image, defaultRegistry) {
		if !hasImagePullSecret(podTemplate, pm.registryAuthSecretName) {
			podTemplate.Spec.ImagePullSecrets = append(podTemplate.Spec.ImagePullSecrets, corev1.LocalObjectReference{
				Name: pm.registryAuthSecretName,
			})
		}
	}
}

// Patches the expected port, and the original entrypoint and image of the kpt function into the function container
func (pm *podManager) patchNewPodContainer(pod *corev1.PodTemplateSpec, de DigestAndEntrypoint, image string) error {
	var patchedContainer bool
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		if container.Name == functionContainerName {
			container.Args = append(container.Args,
				"--port", defaultWrapperServerPort,
				"--max-request-body-size", strconv.Itoa(pm.maxGrpcMessageSize),
				"--",
			)
			container.Args = append(container.Args, de.Entrypoint...)
			container.Image = image
			patchedContainer = true
		}
	}
	if !patchedContainer {
		return fmt.Errorf("failed to find the %v container in the pod", functionContainerName)
	}
	return nil
}

// Patch labels and annotations so the cache manager can keep track of the pod
func (pm *podManager) patchNewPodMetadata(pod *corev1.PodTemplateSpec, podId string, templateVersion string) {
	pod.Namespace = pm.namespace
	annotations := pod.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[templateVersionAnnotation] = templateVersion
	pod.Annotations = annotations

	labels := pod.Labels
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[krmFunctionImageLabel] = podId
	pod.Labels = labels
}

// getServiceUrlOnceEndpointActive retrieves the service full FQDN Url once the backend PoD is
// ready and Endpoint is active.
// Returned URL is in the format <service-name>.<namespace>.svc.cluster.local
func (pm *podManager) getServiceUrlOnceEndpointActive(ctx context.Context, serviceKey client.ObjectKey, podKey client.ObjectKey) (string, error) {
	var service corev1.Service
	var pod corev1.Pod
	//nolint:staticcheck
	var endpoint corev1.Endpoints
	var podReady = false

	if e := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, pm.podReadyTimeout, true, func(ctx context.Context) (done bool, err error) {
		if !podReady {
			err = pm.kubeClient.Get(ctx, podKey, &pod)
			if err != nil {
				return false, err
			}

			if pod.Status.Phase != "Running" {
				return false, nil
			}

			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					podReady = true
				}
			}
			return false, nil
		}

		podIP := pod.Status.PodIP

		if err := pm.kubeClient.Get(ctx, serviceKey, &service); err != nil {
			return false, err
		}

		// Wait till the service has an active endpoint
		if err := pm.kubeClient.Get(ctx, serviceKey, &endpoint); err != nil {
			return false, err
		}

		if len(endpoint.Subsets) == 0 || len(endpoint.Subsets[0].Addresses) == 0 {
			return false, nil
		}

		// Log warning if service has more than one endpoint; there should be just 1 function pod
		if len(endpoint.Subsets[0].Addresses) != 1 {
			klog.Warningf("Service %s/%s has more than one endpoint: %+v", serviceKey.Namespace, serviceKey.Name, endpoint.Subsets)
		}

		// Try to detect if there are any stuck pods
		if len(endpoint.Subsets[0].Addresses) > 1 {
			err = pm.removeStuckPod(ctx, &service, podIP)
			if err != nil {
				return false, err
			}
			// Re-fetch the endpoint after removing the stuck pod to get the updated endpoint list
			if err := pm.kubeClient.Get(ctx, serviceKey, &endpoint); err != nil {
				return false, err
			}
			// Check if endpoint still exists after cleanup
			if len(endpoint.Subsets) == 0 || len(endpoint.Subsets[0].Addresses) == 0 {
				return false, nil
			}
		}

		endpointIP := endpoint.Subsets[0].Addresses[0].IP
		if podIP != endpointIP {
			klog.Warningf("pod IP %s does not match service endpoint IP %s", podIP, endpointIP)
			return false, fmt.Errorf("pod IP %s does not match service endpoint IP %s", podIP, endpointIP)
		}

		return true, nil
	}); e != nil {
		return "", fmt.Errorf("error occurred when waiting the pod and service to be ready. If the error is caused by timeout, you may want to examine the pod/service in namespace %q. Error: %w", pm.namespace, e)
	}

	return service.Name + "." + service.Namespace + serviceDnsNameSuffix, nil
}

func podID(image, hash, postFix string) (string, error) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", fmt.Errorf("unable to parse image reference %v: %w", image, err)
	}

	// repoName will be something like ghcr.io/kptdev/krm-functions-catalog/...
	repoName := ref.Context().Name()
	tag := strings.ReplaceAll(ref.Identifier(), ".", "")
	parts := strings.Split(repoName, "/")
	name := strings.ReplaceAll(parts[len(parts)-1], "_", "-")
	return fmt.Sprintf("%v-%v-%v-%v", name, tag, postFix, hash[:8]), nil
}

func isPodTemplateSameVersion(pod *corev1.Pod, templateVersion string) bool {
	currVersion, found := pod.Annotations[templateVersionAnnotation]
	return found && currVersion == templateVersion
}

func (pm *podManager) findPodsForService(ctx context.Context, svc *corev1.Service) ([]corev1.Pod, error) {
	selector := labels.SelectorFromSet(svc.Spec.Selector)

	var podList corev1.PodList
	if err := pm.kubeClient.List(ctx, &podList, &client.ListOptions{
		Namespace:     svc.Namespace,
		LabelSelector: selector,
	}); err != nil {
		return nil, err
	}

	return podList.Items, nil
}

func (pm *podManager) removeStuckPod(ctx context.Context, service *corev1.Service, expectedIP string) error {
	//nolint:staticcheck
	var endpoint corev1.Endpoints
	var err error
	podList, err := pm.findPodsForService(ctx, service)
	if err != nil {
		return err
	}
	indexToRemove := 0
	for i := 1; i < len(podList); i++ {
		if podList[i].CreationTimestamp.Before(&podList[indexToRemove].CreationTimestamp) {
			indexToRemove = i
		}
	}

	err = pm.kubeClient.Delete(ctx, &podList[indexToRemove])
	if err != nil {
		return fmt.Errorf("unable to delete stuck pod: %w", err)
	}

	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, pm.podReadyTimeout, true, func(ctx context.Context) (bool, error) {
		err = pm.kubeClient.Get(ctx, client.ObjectKeyFromObject(service), &endpoint)
		if err != nil {
			return false, err
		}
		if len(endpoint.Subsets) == 0 || len(endpoint.Subsets[0].Addresses) == 0 {
			return false, nil
		}
		return endpoint.Subsets[0].Addresses[0].IP == expectedIP, nil
	})
	return err
}
