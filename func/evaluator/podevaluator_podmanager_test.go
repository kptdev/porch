// Copyright 2025-2026 The kpt Authors
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
	"bytes"
	"context"
	"encoding/gob"
	"flag"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/kptdev/porch/func/types"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	pb "github.com/kptdev/porch/func/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	defaultImageName               = "apply-replacements"
	defaultPodName                 = "apply-replacements-latest-1-5245a527"
	defaultNamespace               = "porch-fn-system"
	defaultServiceName             = defaultPodName
	defaultEndpointName            = defaultServiceName
	defaultFunctionImageLabel      = defaultPodName
	defaultWrapperServerImage      = "wrapper-server"
	defaultPodIP                   = "10.10.10.10"
	defaultServiceIP               = "20.10.10.10"
	defaultFunctionPodTemplateName = "function-pod-template"
	defaultRegistryAuthSecret      = "authsecret"
)

type fakeFunctionEvalServer struct {
	pb.UnimplementedFunctionEvaluatorServer
	evalFunc func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error)
	port     string
}

func (f *fakeFunctionEvalServer) EvaluateFunction(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
	return f.evalFunc(ctx, req)
}

func (f *fakeFunctionEvalServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", ":"+f.port)

	if err != nil {
		return err
	}

	server := grpc.NewServer()
	pb.RegisterFunctionEvaluatorServer(server, f)
	//nolint:errcheck
	go server.Serve(lis)

	go func() {
		<-ctx.Done()
		server.GracefulStop()
		lis.Close()
	}()
	return nil
}

func withGetInterceptor(podStatus corev1.PodStatus) func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		err := c.Get(ctx, key, obj, opts...)
		if err != nil {
			return err
		}

		var latest corev1.Pod
		if err := c.Get(ctx, key, &latest); err == nil {
			upd := latest.DeepCopy()
			upd.Status = podStatus
			err = c.Status().Update(ctx, upd)
			if err != nil {
				return err
			}
		}

		return nil
	}
}

func TestPodManager(t *testing.T) {

	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", "5"})

	defaultSuccessEvalFunc := func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return &pb.EvaluateFunctionResponse{ResourceList: []byte("thisShouldBeKRM"), Log: []byte("Success")}, nil
	}

	defaultImageMetadataCache := map[string]*DigestAndEntrypoint{
		defaultImageName: {
			Digest:     "5245a52778d684fa698f69861fb2e058b308f6a74fed5bf2fe77d97bad5e071c",
			Entrypoint: []string{"/" + defaultImageName},
		},
	}

	defaultPodObjectMeta := metav1.ObjectMeta{
		Name:      defaultPodName,
		Namespace: defaultNamespace,
		Labels: map[string]string{
			krmFunctionImageLabel: defaultFunctionImageLabel,
		},
	}

	deletionInProgessPodObjectMeta := metav1.ObjectMeta{}
	deepCopyObject(&defaultPodObjectMeta, &deletionInProgessPodObjectMeta)
	deletionInProgessPodObjectMeta.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	deletionInProgessPodObjectMeta.Finalizers = []string{"test-finalizer"}

	defaultPodSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "function",
				Image: defaultImageName,
			},
		},
	}

	podStatusRunning := corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		},
		PodIP: defaultPodIP,
	}

	podStatusRunningDifferentIP := corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		},
		PodIP: "30.30.30.30",
	}

	podStatusNotRunning := corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
		},
		PodIP: "",
	}

	podStatusFailed := corev1.PodStatus{
		Phase: corev1.PodFailed,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
		},
		PodIP: "",
	}

	podStatusPending := corev1.PodStatus{
		Phase: corev1.PodPending,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodScheduled,
				Status: corev1.ConditionTrue,
			},
		},
		PodIP: "",
	}

	defaultPodObject := &corev1.Pod{
		ObjectMeta: defaultPodObjectMeta,
		Spec:       defaultPodSpec,
		Status:     podStatusRunning,
	}

	deletionInProgressPodObject := &corev1.Pod{
		ObjectMeta: deletionInProgessPodObjectMeta,
		Spec:       defaultPodSpec,
		Status:     podStatusNotRunning,
	}

	failedPodObject := &corev1.Pod{
		ObjectMeta: deletionInProgessPodObjectMeta,
		Spec:       defaultPodSpec,
		Status:     podStatusFailed,
	}

	defaultServiceObject := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultServiceName,
			Namespace: defaultNamespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: defaultServiceIP,
		},
	}

	defaultEndpointObject := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultEndpointName,
			Namespace: defaultNamespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: defaultPodIP,
						TargetRef: &corev1.ObjectReference{
							Name:      defaultPodName,
							Namespace: defaultNamespace,
						},
					},
				},
			},
		},
	}

	basePodTemplate := inlineBasePodTemplate.DeepCopy()
	baseServiceTemplate := inlineBaseServiceTemplate.DeepCopy()

	// Fake client. When Pod creation is invoked, it creates the Pod if not present
	// When Service creation is invoked, it creates endpoint object in additon to service
	fakeClientCreateFixInterceptor := func(ctx context.Context, kubeClient client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetObjectKind().GroupVersionKind().Kind == "Pod" {
			var canary corev1.Pod
			err := kubeClient.Get(ctx, client.ObjectKeyFromObject(obj), &canary)
			if err != nil {
				if errors.IsNotFound(err) {
					err = kubeClient.Create(ctx, obj)
					if err != nil {
						return err
					}

					return nil
				}
				return err
			}
		}

		if obj.GetObjectKind().GroupVersionKind().Kind == "Service" {
			var canary corev1.Service
			err := kubeClient.Get(ctx, client.ObjectKeyFromObject(obj), &canary)
			if err != nil {
				if errors.IsNotFound(err) {
					defaultServiceObject.ResourceVersion = ""
					err = kubeClient.Create(ctx, defaultServiceObject)
					if err != nil {
						return err
					}

					defaultEndpointObject.ResourceVersion = ""
					err = kubeClient.Create(ctx, defaultEndpointObject)
					if err != nil {
						return err
					}
					return nil
				}
				return err
			}
		}
		return nil
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = configapi.AddToScheme(scheme)

	tests := []struct {
		name               string
		expectFail         bool
		skip               bool
		kubeClient         client.WithWatch
		namespace          string
		wrapperServerImage string
		imageMetadataCache map[string]*DigestAndEntrypoint
		evalFunc           func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error)
		functionImage      string
		managerNamespace   string
		podPatchAfter      time.Duration
	}{
		{
			name:          "Pod is in deleting state",
			skip:          false,
			expectFail:    false,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithObjects([]client.Object{
				deletionInProgressPodObject,
				defaultServiceObject,
				defaultEndpointObject,
			}...).WithInterceptorFuncs(interceptor.Funcs{
				Create: fakeClientCreateFixInterceptor,
				Get:    withGetInterceptor(podStatusRunning),
			}).WithStatusSubresource(&corev1.Pod{}).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:          "Create a new pod and new service",
			skip:          false,
			expectFail:    false,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: fakeClientCreateFixInterceptor,
					Get:    withGetInterceptor(podStatusRunning),
				}).
				WithStatusSubresource(&corev1.Pod{}).
				Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:          "Create a new pod but service is existing",
			skip:          false,
			expectFail:    false,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Create: fakeClientCreateFixInterceptor,
				Get:    withGetInterceptor(podStatusRunning),
			}).WithObjects([]client.Object{
				defaultServiceObject,
				defaultEndpointObject,
			}...).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:          "Create a new pod but service does not get a new endpoint",
			skip:          false,
			expectFail:    true,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Create: fakeClientCreateFixInterceptor,
				Get:    withGetInterceptor(podStatusRunning),
			}).WithObjects([]client.Object{
				defaultServiceObject,
			}...).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:          "Create a new pod but endpoint ip does not match pod ip",
			skip:          false,
			expectFail:    true,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Create: fakeClientCreateFixInterceptor,
				Get:    withGetInterceptor(podStatusRunningDifferentIP),
			}).WithObjects([]client.Object{
				defaultServiceObject,
				defaultEndpointObject,
			}...).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:          "Pod startup takes too long",
			skip:          false,
			expectFail:    true,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: withGetInterceptor(podStatusPending),
			}).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:          "Pod startup takes some time",
			skip:          false,
			expectFail:    true,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: withGetInterceptor(podStatusPending),
			}).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
			podPatchAfter:      100 * time.Millisecond,
		},
		{
			name:          "Fail pod creation",
			skip:          false,
			expectFail:    true,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if obj.GetObjectKind().GroupVersionKind().Kind == "Pod" {
						return apierrors.NewInternalError(fmt.Errorf("Faked error"))
					}
					return nil
				},
			}).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{ //This is current behavior, but is it correct?
			name:          "If listing pods fail, try to create a new one",
			skip:          false,
			expectFail:    false,
			functionImage: "apply-replacements",
			kubeClient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: withGetInterceptor(podStatusRunning),
				List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					_, ok := list.(*corev1.PodList)
					if ok {
						return apierrors.NewInternalError(fmt.Errorf("Faked error"))
					}
					return nil
				},
			}).WithObjects([]client.Object{
				defaultPodObject,
				defaultServiceObject,
				defaultEndpointObject,
			}...).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:               "Has invalid function image name",
			skip:               false,
			expectFail:         true,
			functionImage:      "invalid@ociref.com",
			kubeClient:         fake.NewClientBuilder().Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:               "Invalid namespace name",
			skip:               false,
			expectFail:         true,
			functionImage:      defaultImageName,
			kubeClient:         fake.NewClientBuilder().Build(),
			namespace:          "not a valid namespace",
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
		{
			name:               "Function template configmap not found",
			skip:               false,
			expectFail:         true,
			functionImage:      defaultImageName,
			kubeClient:         fake.NewClientBuilder().Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
			managerNamespace:   defaultManagerNamespace,
		},
		{
			name:          "Function template generates pod",
			skip:          false,
			expectFail:    false,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(
					basePodTemplate,
					baseServiceTemplate,
					defaultEndpointObject,
				).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: withGetInterceptor(podStatusRunning),
				}).
				Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
			managerNamespace:   defaultManagerNamespace,
		},
		{
			name:          "Function template update is applied when pod is requested",
			skip:          false,
			expectFail:    false,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(
					basePodTemplate,
					baseServiceTemplate,
					defaultPodObject,
					defaultEndpointObject,
				).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: withGetInterceptor(podStatusRunning),
				}).
				Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
			managerNamespace:   defaultManagerNamespace,
		},
		{
			name:          "Failed pod is deleted and new one is created",
			skip:          false,
			expectFail:    false,
			functionImage: defaultImageName,
			kubeClient: fake.NewClientBuilder().WithObjects([]client.Object{
				failedPodObject,
				defaultServiceObject,
				defaultEndpointObject,
			}...).WithInterceptorFuncs(interceptor.Funcs{
				Create: fakeClientCreateFixInterceptor,
				Get:    withGetInterceptor(podStatusRunning),
			}).Build(),
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: defaultImageMetadataCache,
			evalFunc:           defaultSuccessEvalFunc,
		},
	}

	fakeServer := &fakeFunctionEvalServer{
		port: defaultWrapperServerPort,
	}
	srvCtx := context.WithoutCancel(context.Background())

	err := fakeServer.Start(srvCtx)
	if err != nil {
		t.Errorf("Failed to set up grpc server for testing %v", err)
		t.FailNow()
	}
	defer srvCtx.Done()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.SkipNow()
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			//Set up the pod manager
			podReadyCh := make(chan *PodReadyResponse)
			pm := &podManager{
				kubeClient:         tt.kubeClient,
				namespace:          tt.namespace,
				wrapperServerImage: tt.wrapperServerImage,
				imageMetadataCache: sync.Map{},
				podReadyCh:         podReadyCh,
				podReadyTimeout:    5 * time.Second,
				managerNamespace:   tt.managerNamespace,

				maxGrpcMessageSize: 4 * 1024 * 1024,

				enablePrivateRegistries: false,
				registryAuthSecretPath:  "/var/tmp/config-secret/.dockerconfigjson",
				registryAuthSecretName:  "auth-secret",

				enablePrivateRegistriesTls: false,
				tlsSecretPath:              "/var/tmp/tls-secret/",
			}

			for k, v := range tt.imageMetadataCache {
				pm.imageMetadataCache.Store(k, v)
			}

			fakeServer.evalFunc = tt.evalFunc

			podConfig := &configapi.PodExecutorConfig{}

			//Execute the function under test
			go pm.getFuncEvalPodClient(ctx, tt.functionImage, 1, podConfig, false)

			cc := <-podReadyCh
			if cc.Err != nil && !tt.expectFail {
				assert.NoError(t, cc.Err, "Expected to get ready pod")
			} else if cc.Err == nil {
				if tt.expectFail {
					assert.Fail(t, "Expected to get error, got ready pod")
				}
				var pod corev1.Pod
				err := tt.kubeClient.Get(ctx, *cc.PodKey, &pod)
				assert.NoError(t, err, "Failed to get pod")

				assert.True(t, strings.HasPrefix(pod.Labels[krmFunctionImageLabel], tt.functionImage),
					"Expected pod to have label starting with %s, got %s", tt.functionImage, pod.Labels[krmFunctionImageLabel])
				assert.Equal(t, tt.functionImage, pod.Spec.Containers[0].Image,
					"Expected pod to have image %s", tt.functionImage)

			}

		})
	}
}

func TestMultipleEndpointsWithStuckPod(t *testing.T) {
	const (
		newPodName = "new-pod"
		oldPodName = "old-pod"
		newPodIP   = "10.255.0.1"
		oldPodIP   = "10.255.0.2"
	)

	podStatusRunningNewIP := corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		},
		PodIP: newPodIP,
	}

	podStatusRunningOldIP := corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		},
		PodIP: oldPodIP,
	}

	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              newPodName,
			Namespace:         defaultNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now()},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "function",
					Image: defaultImageName,
				},
			},
		},
		Status: podStatusRunningNewIP,
	}

	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              oldPodName,
			Namespace:         defaultNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
			Labels: map[string]string{
				"test-label": "test-value",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "function",
					Image: defaultImageName,
				},
			},
		},
		Status: podStatusRunningOldIP,
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultServiceName,
			Namespace: defaultNamespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: defaultServiceIP,
			Selector: map[string]string{
				"test-label": "test-value",
			},
		},
	}

	endpoint := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultEndpointName,
			Namespace: defaultNamespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: oldPodIP,
						TargetRef: &corev1.ObjectReference{
							Name:      oldPodName,
							Namespace: defaultNamespace,
						},
					},
					{
						IP: newPodIP,
						TargetRef: &corev1.ObjectReference{
							Name:      newPodName,
							Namespace: defaultNamespace,
						},
					},
				},
			},
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithObjects(newPod, oldPod, service, endpoint).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if pod, ok := obj.(*corev1.Pod); ok && pod.Name == oldPodName {
					var ep corev1.Endpoints
					epKey := client.ObjectKey{Namespace: defaultNamespace, Name: defaultEndpointName}
					if err := c.Get(ctx, epKey, &ep); err == nil {
						updated := ep.DeepCopy()
						updated.Subsets = []corev1.EndpointSubset{
							{
								Addresses: []corev1.EndpointAddress{
									{
										IP: newPodIP,
										TargetRef: &corev1.ObjectReference{
											Name:      newPodName,
											Namespace: defaultNamespace,
										},
									},
								},
							},
						}
						_ = c.Update(ctx, updated)
					}
				}
				return c.Delete(ctx, obj, opts...)
			},
		}).
		Build()

	pm := &podManager{
		kubeClient:      kubeClient,
		namespace:       defaultNamespace,
		podReadyTimeout: 1 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	serviceKey := client.ObjectKey{Namespace: defaultNamespace, Name: defaultServiceName}
	podKey := client.ObjectKey{Namespace: defaultNamespace, Name: newPodName}

	serviceURL, err := pm.getServiceUrlOnceEndpointActive(ctx, serviceKey, podKey)

	assert.NoError(t, err, "Expected getServiceUrlOnceEndpointActive to succeed")
	assert.NotEmpty(t, serviceURL, "Expected non-empty service URL")

	var deletedPod corev1.Pod
	err = kubeClient.Get(ctx, client.ObjectKey{Namespace: defaultNamespace, Name: oldPodName}, &deletedPod)
	assert.Error(t, err, "Expected old pod to be deleted, but it still exists")

	var finalEndpoint corev1.Endpoints
	err = kubeClient.Get(ctx, client.ObjectKey{Namespace: defaultNamespace, Name: defaultEndpointName}, &finalEndpoint)
	require.NoError(t, err, "Failed to get final endpoint")

	if assert.NotEmpty(t, finalEndpoint.Subsets, "Expected endpoint to have subsets") &&
		assert.NotEmpty(t, finalEndpoint.Subsets[0].Addresses, "Expected endpoint to have addresses") {
		assert.Len(t, finalEndpoint.Subsets[0].Addresses, 1, "Expected endpoint to have exactly 1 address")
		assert.Equal(t, newPodIP, finalEndpoint.Subsets[0].Addresses[0].IP, "Expected endpoint IP to match new pod IP")
	}
}

// Fake client handles pod patches incorrectly in case the pod doesn't exist
//
//nolint:unused
func fakeClientPatchFixInterceptor(ctx context.Context, kubeClient client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if obj.GetObjectKind().GroupVersionKind().Kind == "Pod" {
		var canary corev1.Pod
		err := kubeClient.Get(ctx, client.ObjectKeyFromObject(obj), &canary)
		if err != nil {
			if apierrors.IsNotFound(err) {
				err = kubeClient.Create(ctx, obj)
				if err != nil {
					return err
				}
				return nil
			}
			return err
		}
	}
	return nil
}

func marshalToYamlOrPanic(obj interface{}) []byte {
	data, err := yaml.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return data
}

func deepCopyObject(in, out interface{}) {
	buf := bytes.Buffer{}
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		panic(err)
	}

	if err := gob.NewDecoder(&buf).Decode(out); err != nil {
		panic(err)
	}
}
