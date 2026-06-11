/*
 Copyright 2025 The kpt Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 You may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package evaluator

import (
	"context"
	"flag"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	. "github.com/kptdev/porch/func/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestPodCacheManager(t *testing.T) {
	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", "5"})

	const mockTemplateRV = "12345"

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
		Annotations: map[string]string{
			templateVersionAnnotation: mockTemplateRV,
		},
	}

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

	defaultPodObject := &corev1.Pod{
		ObjectMeta: defaultPodObjectMeta,
		Spec:       defaultPodSpec,
		Status:     podStatusRunning,
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

	emptyEndpointObject := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultEndpointName,
			Namespace: defaultNamespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{},
			},
		},
	}

	basePodTemplate := inlineBasePodTemplate.DeepCopy()
	basePodTemplate.ObjectMeta.ResourceVersion = mockTemplateRV
	basePodTemplate.Namespace = defaultNamespace
	basePodTemplate.Template.Spec.InitContainers[0].Image = defaultWrapperServerImage

	baseServiceTemplate := inlineBaseServiceTemplate.DeepCopy()
	baseServiceTemplate.ObjectMeta.ResourceVersion = mockTemplateRV
	baseServiceTemplate.Namespace = defaultNamespace

	// Fake client. When Pod creation is invoked, it creates the Pod if not present and patches it to Running status
	// When Service creation is invoked, it creates endpoint object in additon to service
	fakeClientCreateFixInterceptor := func(ctx context.Context, kubeClient client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetObjectKind().GroupVersionKind().Kind == "Pod" {
			var canary corev1.Pod
			err := kubeClient.Get(ctx, client.ObjectKeyFromObject(obj), &canary)
			if err != nil {
				if apierrors.IsNotFound(err) {
					obj.SetName(defaultPodName)
					obj.SetGenerateName("")
					err = kubeClient.Create(ctx, obj)
					if err != nil {
						return err
					}

					pod := obj.(*corev1.Pod)
					pod.Status = podStatusRunning
					err = kubeClient.Status().Update(context.Background(), pod)

					if err != nil {
						t.Errorf("Failed to patch pod: %v", err)

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
				if apierrors.IsNotFound(err) {
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

	fakeClientListPodsInterceptor := func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		if pl, ok := list.(*corev1.PodList); ok {
			pl.Items = []corev1.Pod{
				*defaultPodObject,
			}
			return nil
		}

		return client.List(ctx, list, nil)
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = configapi.AddToScheme(scheme)

	buildKubeClient := func(objects ...client.Object) client.WithWatch {
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithInterceptorFuncs(interceptor.Funcs{Create: fakeClientCreateFixInterceptor}).
			Build()
	}

	serviceUrl := defaultServiceName + "." + defaultNamespace + serviceDnsNameSuffix

	address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
	grpcClient, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to create grpc client: %v", err)
	}

	blankCache := make(map[string]*FunctionInfo)

	pData := PodData{
		Image:          defaultImageName,
		GrpcConnection: grpcClient,
		PodKey:         ptr.To(client.ObjectKeyFromObject(defaultPodObject)),
		ServiceKey:     ptr.To(client.ObjectKeyFromObject(defaultServiceObject)),
	}

	funcPodInfo := NewPodInfo(nil)
	funcPodInfo.PodData = &pData
	funcPodInfo.ConcurrentEvaluations.Add(1)

	funcInfo := FunctionInfo{
		Pods: []FunctionPodInfo{funcPodInfo},
	}

	functionWithDefaultPod := make(map[string]*FunctionInfo)
	functionWithDefaultPod[defaultImageName] = &funcInfo

	// These tests focus on the pod management logic in the pod cache manager covering
	// the mechanism of creating pod/service corresponding to invoked function,
	// waiting for it be ready via channel and managing the cache.
	podManagementTests := []struct {
		name         string
		expectFail   bool
		skip         bool
		kubeClient   client.WithWatch
		functions    map[string]*FunctionInfo
		expectedLog  string
		skipRetrieve bool
	}{
		{
			name:       "Pod Exists but not in Cache",
			skip:       false,
			expectFail: false,
			functions:  blankCache,
			kubeClient: fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(
					basePodTemplate,
					defaultPodObject,
					defaultServiceObject,
					defaultEndpointObject,
				).
				WithInterceptorFuncs(interceptor.Funcs{List: fakeClientListPodsInterceptor}).
				Build(),
			expectedLog: "retrieved function evaluator pod porch-fn-system/apply-replacements-latest-1-5245a527 for apply-replacements",
		},
		{
			name:         "Pod Exists and in Cache",
			skip:         false,
			expectFail:   false,
			functions:    functionWithDefaultPod,
			kubeClient:   buildKubeClient(basePodTemplate, defaultPodObject, defaultServiceObject, defaultEndpointObject),
			expectedLog:  "Queuing request for apply-replacements on pod",
			skipRetrieve: true,
		},
		{
			name:        "Pod does not Exists and not in Cache",
			skip:        false,
			expectFail:  false,
			functions:   blankCache,
			kubeClient:  buildKubeClient(),
			expectedLog: "Scaling up for image apply-replacements. No idle pods available. Starting a new pod.",
		},
		{
			name:       "Pod does not Exists but in Cache",
			skip:       false,
			expectFail: false,
			functions:  functionWithDefaultPod,
			kubeClient: fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{Create: fakeClientCreateFixInterceptor}).
				Build(),
			expectedLog: "Removing deleted pod from cache for image apply-replacements",
		},
		{
			name:       "Pod does not Exists but Service Exists and not in Cache",
			skip:       false,
			expectFail: false,
			functions:  blankCache,
			kubeClient: buildKubeClient(defaultServiceObject, defaultEndpointObject),
		},
		{
			name:       "Pod Exists but Service does not Exists and not in Cache",
			skip:       false,
			expectFail: false,
			functions:  blankCache,
			kubeClient: buildKubeClient(basePodTemplate, defaultPodObject),
		},
	}

	//Set up the podmanager and podcachemanager
	requestCh := make(chan *ConnectionRequest)
	podReadyCh := make(chan *PodReadyResponse)

	pm := &podManager{
		namespace:               defaultNamespace,
		wrapperServerImage:      defaultWrapperServerImage,
		imageMetadataCache:      sync.Map{},
		podReadyTimeout:         2 * time.Second,
		managerNamespace:        defaultNamespace,
		enablePrivateRegistries: false,
		podReadyCh:              podReadyCh,
	}

	for k, v := range defaultImageMetadataCache {
		pm.imageMetadataCache.Store(k, v)
	}

	functionConfigStore := functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function")

	pcm := &podCacheManager{
		// Setting to 5 minutes to avoid GC invocation
		gcScanInterval:             5 * time.Minute,
		podTTL:                     10 * time.Minute,
		podReadyCh:                 podReadyCh,
		podManager:                 pm,
		connectionRequestCh:        requestCh,
		maxWaitlistLength:          1,
		maxParallelPodsPerFunction: 1,
		functionConfigMap:          functionConfigStore,
	}

	go pcm.podCacheManager(t.Context())

	for _, tt := range podManagementTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.SkipNow()
			}

			logger := ktesting.NewLogger(t, ktesting.NewConfig(ktesting.BufferLogs(true)))
			defer klog.ClearLogger() // restore global logger after test
			klog.SetLogger(logger)

			pcm.functions = make(map[string]*FunctionInfo)
			for k, v := range tt.functions {
				pcm.functions[k] = v
			}
			pcm.podManager.kubeClient = tt.kubeClient

			if !tt.skipRetrieve {
				err = pcm.retrieveFunctionPods(context.Background())
				if err != nil {
					t.Logf("Something happened %v", err)
				}

			}

			clientConn := make(chan *ConnectionResponse)
			requestCh <- &ConnectionRequest{defaultImageName, clientConn}

			select {
			case cc := <-clientConn:
				if !tt.expectFail && cc.Err != nil {
					t.Errorf("Expected to get client connection, got error: %v", cc.Err)
				} else if tt.expectFail && cc.Err == nil {
					t.Errorf("Expected to get error, got client connection")
				} else if cc.Err == nil {
					if cc.PodData.GrpcConnection == nil {
						t.Errorf("Expected to get grpc client, got nil")
					}
				}

			case <-time.After(5 * time.Second):
				t.Errorf("Timed out waiting for client connection")
			}
			if under, ok := logger.GetSink().(ktesting.Underlier); ok {
				if !strings.Contains(under.GetBuffer().String(), tt.expectedLog) {
					t.Fatalf("missing expected log line")
				}
			}
		})
	}

	oldPodObject := &corev1.Pod{}
	deepCopyObject(defaultPodObject, oldPodObject)

	newPodObject := &corev1.Pod{}
	deepCopyObject(defaultPodObject, newPodObject)

	podObjectInvalidRetentionTimestamp := &corev1.Pod{}
	deepCopyObject(defaultPodObject, podObjectInvalidRetentionTimestamp)

	funcExpiredPodInfo := NewPodInfo(nil)
	funcExpiredPodInfo.PodData = &pData
	funcExpiredPodInfo.LastActivity = time.Now().Add(-11 * time.Minute)

	funcExpiredInfo := FunctionInfo{
		Pods: []FunctionPodInfo{funcExpiredPodInfo},
	}

	functionWithExpiredPod := make(map[string]*FunctionInfo)
	functionWithExpiredPod[defaultImageName] = &funcExpiredInfo

	// These tests focus on the pod cleanup / garbage collection logic in the pod
	// cache manager covering automated deletion of pod/service after retention period
	podCleanupTests := []struct {
		name                   string
		expectFail             bool
		skip                   bool
		kubeClient             client.WithWatch
		functions              map[string]*FunctionInfo
		podShouldBeDeleted     bool
		serviceShouldBeDeleted bool
		cacheShouldBeEmpty     bool
	}{
		{
			name:                   "Garbage Collector - Expired Pod Exists and cleaned up",
			expectFail:             false,
			skip:                   false,
			kubeClient:             buildKubeClient(oldPodObject, defaultServiceObject, emptyEndpointObject),
			functions:              functionWithExpiredPod,
			podShouldBeDeleted:     true,
			serviceShouldBeDeleted: true,
			cacheShouldBeEmpty:     true,
		},
		{
			name:                   "Garbage Collector - New Pod Exists and not cleaned up",
			expectFail:             false,
			skip:                   false,
			kubeClient:             buildKubeClient(newPodObject, defaultServiceObject, defaultEndpointObject),
			functions:              functionWithDefaultPod,
			podShouldBeDeleted:     false,
			serviceShouldBeDeleted: false,
			cacheShouldBeEmpty:     false,
		},
		{
			name:                   "Garbage Collector - Non Annotated Pod Exists and not cleaned up",
			expectFail:             false,
			skip:                   false,
			kubeClient:             buildKubeClient(defaultPodObject, defaultServiceObject, defaultEndpointObject),
			functions:              functionWithDefaultPod,
			podShouldBeDeleted:     false,
			serviceShouldBeDeleted: false,
			cacheShouldBeEmpty:     false,
		},
		{
			name:                   "Garbage Collector - Annotated Pod Exists with invalid timestamp and not cleaned up",
			expectFail:             false,
			skip:                   false,
			kubeClient:             buildKubeClient(podObjectInvalidRetentionTimestamp, defaultServiceObject, defaultEndpointObject),
			functions:              functionWithDefaultPod,
			podShouldBeDeleted:     false,
			serviceShouldBeDeleted: false,
			cacheShouldBeEmpty:     false,
		},
	}

	for _, tt := range podCleanupTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.SkipNow()
			}

			pcm.functions = make(map[string]*FunctionInfo)
			for k, v := range tt.functions {
				pcm.functions[k] = v
			}

			pcm.podManager.kubeClient = tt.kubeClient

			// Trigger garbage collection
			pcm.garbageCollector()

			// Wait for 1 second as garbage collector purges pods in goroutine
			<-time.After(1 * time.Second)

			var pod corev1.Pod
			err := pcm.podManager.kubeClient.Get(context.Background(), client.ObjectKeyFromObject(defaultPodObject), &pod)
			if tt.podShouldBeDeleted && err == nil {
				t.Errorf("Expected pod to be deleted, but it still exists")
			} else if !tt.podShouldBeDeleted && err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("Expected pod to exist, but got error: %v", err)
			}

			var service corev1.Service
			err = pcm.podManager.kubeClient.Get(context.Background(), client.ObjectKeyFromObject(defaultServiceObject), &service)
			if tt.serviceShouldBeDeleted && err == nil {
				t.Errorf("Expected service to be deleted, but it still exists")
			} else if !tt.serviceShouldBeDeleted && err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("Expected service to exist, but got error: %v", err)
			}

			if tt.cacheShouldBeEmpty {
				if len(pcm.functions) != 0 {
					t.Errorf("Expected cache to be empty, but it has %d entries", len(pcm.functions))
				}
			} else {
				if len(pcm.functions) == 0 {
					t.Errorf("Expected cache to have entries, but it is empty")
				} else if len(pcm.functions) != 1 {
					t.Errorf("Expected cache to have 1 entry, but it has %d entries", len(pcm.functions))
				}
			}
		})
	}
}
