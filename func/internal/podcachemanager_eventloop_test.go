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

package internal

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	fnconf "github.com/kptdev/porch/controllers/functionconfigs/reconciler"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// newTestEventLoopPCM creates a podCacheManager with unbuffered channels suitable
// for deterministic event loop testing. The podManager's podReadyCh is the same as
// the pcm's podReadyCh so that getFuncEvalPodClient sends results to the event loop.
func newTestEventLoopPCM(kubeClient client.Client) (*podCacheManager, chan *connectionRequest, chan *podReadyResponse) {
	reqCh := make(chan *connectionRequest)
	readyCh := make(chan *podReadyResponse)
	pcm := &podCacheManager{
		gcScanInterval:             5 * time.Minute,
		podTTL:                     10 * time.Minute,
		connectionRequestCh:        reqCh,
		podReadyCh:                 readyCh,
		functions:                  map[string]*functionInfo{},
		maxWaitlistLength:          2,
		maxParallelPodsPerFunction: 1,
		functionConfigMap:          fnconf.NewFunctionConfigStore(runneroptions.GHCRImagePrefix, "/functions"),
		podManager: &podManager{
			kubeClient:         kubeClient,
			namespace:          defaultNamespace,
			wrapperServerImage: defaultWrapperServerImage,
			imageMetadataCache: sync.Map{},
			podReadyCh:         readyCh,
			podReadyTimeout:    2 * time.Second,
			managerNamespace:   defaultNamespace,
		},
	}
	return pcm, reqCh, readyCh
}

// ---------- Event Loop Tests ----------

func TestEventLoop_PodReadyEmptyImage(t *testing.T) {
	kubeClient := fake.NewClientBuilder().Build()
	pcm, _, readyCh := newTestEventLoopPCM(kubeClient)

	// Pre-populate a pending pod for "test-image" BEFORE starting the event loop
	waitCh := make(chan *connectionResponse, 1)
	pcm.functions["test-image"] = &functionInfo{
		pods: []functionPodInfo{NewPodInfo(waitCh)},
	}

	go pcm.podCacheManager(t.Context())

	// Send podReady with empty image → should be logged and skipped
	readyCh <- &podReadyResponse{podData: podData{image: ""}}

	// Send valid podReady to prove the loop continued past the empty image
	conn, _ := grpc.NewClient("localhost:9446", grpc.WithTransportCredentials(insecure.NewCredentials()))
	podKey := client.ObjectKey{Name: "test-pod", Namespace: defaultNamespace}
	serviceKey := client.ObjectKey{Name: "test-svc", Namespace: defaultNamespace}
	readyCh <- &podReadyResponse{
		podData: podData{
			image:          "test-image",
			grpcConnection: conn,
			podKey:         &podKey,
			serviceKey:     &serviceKey,
		},
	}

	select {
	case resp := <-waitCh:
		assert.NoError(t, resp.err)
		assert.Equal(t, "test-image", resp.image)
	case <-time.After(5 * time.Second):
		t.Fatal("event loop did not process valid podReady after empty image")
	}
}

func TestEventLoop_PodReadyUnknownFunction(t *testing.T) {
	kubeClient := fake.NewClientBuilder().Build()
	pcm, _, readyCh := newTestEventLoopPCM(kubeClient)

	// Pre-populate a pending pod for "known-image" BEFORE starting the event loop
	waitCh := make(chan *connectionResponse, 1)
	pcm.functions["known-image"] = &functionInfo{
		pods: []functionPodInfo{NewPodInfo(waitCh)},
	}

	go pcm.podCacheManager(t.Context())

	// Send podReady for "unknown-image" (not in functions map) → logged, skipped
	conn, _ := grpc.NewClient("localhost:9446", grpc.WithTransportCredentials(insecure.NewCredentials()))
	podKey := client.ObjectKey{Name: "test-pod", Namespace: defaultNamespace}
	serviceKey := client.ObjectKey{Name: "test-svc", Namespace: defaultNamespace}
	readyCh <- &podReadyResponse{
		podData: podData{
			image:          "unknown-image",
			grpcConnection: conn,
			podKey:         &podKey,
			serviceKey:     &serviceKey,
		},
	}

	// Send valid podReady for "known-image" to prove the loop continued
	readyCh <- &podReadyResponse{
		podData: podData{
			image:          "known-image",
			grpcConnection: conn,
			podKey:         &podKey,
			serviceKey:     &serviceKey,
		},
	}

	select {
	case resp := <-waitCh:
		assert.NoError(t, resp.err)
		assert.Equal(t, "known-image", resp.image)
	case <-time.After(5 * time.Second):
		t.Fatal("event loop did not continue after unknown function podReady")
	}
}

func TestEventLoop_PodReadyNoPendingPod(t *testing.T) {
	// Pre-populate with a READY pod (podData != nil) — no pending instances
	podKey := client.ObjectKey{Name: "ready-pod", Namespace: defaultNamespace}
	serviceKey := client.ObjectKey{Name: "ready-svc", Namespace: defaultNamespace}
	serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
	address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
	conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

	k8sPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ready-pod", Namespace: defaultNamespace},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	k8sSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ready-svc", Namespace: defaultNamespace},
	}

	kubeClient := fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build()
	pcm, reqCh, readyCh := newTestEventLoopPCM(kubeClient)

	readyPod := makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0)
	pcm.functions["test-image"] = &functionInfo{
		pods: []functionPodInfo{readyPod},
	}

	go pcm.podCacheManager(t.Context())

	// Send podReady for "test-image" — all pods are ready, no pending instance → logged, skipped
	readyCh <- &podReadyResponse{
		podData: podData{
			image:          "test-image",
			grpcConnection: conn,
			podKey:         &podKey,
			serviceKey:     &serviceKey,
		},
	}

	// Verify the loop continues by sending a connectionRequest
	responseCh := make(chan *connectionResponse, 1)
	reqCh <- &connectionRequest{image: "test-image", responseCh: responseCh}

	select {
	case resp := <-responseCh:
		assert.NoError(t, resp.err)
	case <-time.After(5 * time.Second):
		t.Fatal("event loop stopped after podReady with no pending pod")
	}
}

func TestEventLoop_QueueOnPendingPod(t *testing.T) {
	kubeClient := fake.NewClientBuilder().Build()
	pcm, reqCh, readyCh := newTestEventLoopPCM(kubeClient)

	// Pre-populate with pending pod that has one initial waiter
	initialCh := make(chan *connectionResponse, 1)
	pcm.functions["test-image"] = &functionInfo{
		pods: []functionPodInfo{NewPodInfo(initialCh)},
	}

	go pcm.podCacheManager(t.Context())

	// Send another connectionRequest — should queue on the existing pending pod
	secondCh := make(chan *connectionResponse, 1)
	reqCh <- &connectionRequest{image: "test-image", responseCh: secondCh}

	// Now complete the pod by sending podReady
	conn, _ := grpc.NewClient("localhost:9446", grpc.WithTransportCredentials(insecure.NewCredentials()))
	podKey := client.ObjectKey{Name: "test-pod", Namespace: defaultNamespace}
	serviceKey := client.ObjectKey{Name: "test-svc", Namespace: defaultNamespace}
	readyCh <- &podReadyResponse{
		podData: podData{
			image:          "test-image",
			grpcConnection: conn,
			podKey:         &podKey,
			serviceKey:     &serviceKey,
		},
	}

	// Both waiters should receive successful responses
	select {
	case resp := <-initialCh:
		assert.NoError(t, resp.err)
	case <-time.After(5 * time.Second):
		t.Fatal("initial waiter did not receive response")
	}

	select {
	case resp := <-secondCh:
		assert.NoError(t, resp.err)
	case <-time.After(5 * time.Second):
		t.Fatal("second waiter did not receive response")
	}
}

func TestEventLoop_PodFailedNoRedistribution(t *testing.T) {
	// Interceptor that fails Pod creation
	kubeClient := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.Pod); ok {
					return apierrors.NewInternalError(fmt.Errorf("fake pod create error"))
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()

	pcm, reqCh, _ := newTestEventLoopPCM(kubeClient)

	// Pre-populate imageMetadataCache so imageDigestAndEntrypoint returns instantly
	pcm.podManager.imageMetadataCache.Store("gcr.io/kpt-fn/test-fn:latest", &digestAndEntrypoint{
		digest:     "abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
		entrypoint: []string{"/test-fn"},
	})

	go pcm.podCacheManager(t.Context())

	// Send connectionRequest — triggers scale-up → goroutine → CreatePod fails → error response
	responseCh := make(chan *connectionResponse, 1)
	reqCh <- &connectionRequest{
		image:      "gcr.io/kpt-fn/test-fn:latest",
		responseCh: responseCh,
	}

	select {
	case resp := <-responseCh:
		assert.Error(t, resp.err)
		assert.Contains(t, resp.err.Error(), "fake pod create error")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for error response from failed pod creation")
	}
}

// ---------- retrieveFunctionPods Tests ----------

func TestRetrieveFunctionPods_ListFails(t *testing.T) {
	kubeClient := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.PodList); ok {
					return apierrors.NewInternalError(fmt.Errorf("fake list error"))
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()

	pcm := &podCacheManager{
		functions: map[string]*functionInfo{},
		podManager: &podManager{
			kubeClient:       kubeClient,
			namespace:        defaultNamespace,
			managerNamespace: defaultNamespace,
		},
	}
	// retrieveFunctionPods logs the error but returns nil (graceful degradation)
	err := pcm.retrieveFunctionPods(context.Background())
	assert.NoError(t, err)
}

func TestRetrieveFunctionPods_EmptyPodList(t *testing.T) {
	kubeClient := fake.NewClientBuilder().Build()

	pcm := &podCacheManager{
		functions: map[string]*functionInfo{},
		podManager: &podManager{
			kubeClient:       kubeClient,
			namespace:        defaultNamespace,
			managerNamespace: defaultNamespace,
		},
	}
	err := pcm.retrieveFunctionPods(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, pcm.functions)
}
