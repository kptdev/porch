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
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	. "github.com/kptdev/porch/func/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// makePodInfoWithLoad creates a FunctionPodInfo with the specified concurrent evaluation count.
func makePodInfoWithLoad(load int32) FunctionPodInfo {
	counter := &atomic.Int32{}
	counter.Store(load)
	return FunctionPodInfo{
		ConcurrentEvaluations: counter,
		LastActivity:          time.Now(),
		Waitlist:              []chan<- *ConnectionResponse{},
	}
}

// makeReadyPodInfo creates a FunctionPodInfo with PodData, for testing functions that require a ready pod.
func makeReadyPodInfo(image string, podKey, serviceKey client.ObjectKey, grpcConn *grpc.ClientConn, load int32) FunctionPodInfo {
	counter := &atomic.Int32{}
	counter.Store(load)
	return FunctionPodInfo{
		PodData: &PodData{
			Image:          image,
			PodKey:         &podKey,
			ServiceKey:     &serviceKey,
			GrpcConnection: grpcConn,
		},
		ConcurrentEvaluations: counter,
		LastActivity:          time.Now(),
		Waitlist:              []chan<- *ConnectionResponse{},
	}
}

func TestFindBestPod(t *testing.T) {
	pcm := &podCacheManager{}

	tests := []struct {
		name             string
		fn               *FunctionInfo
		expectedIdx      int
		expectedWaitlist int
	}{
		{
			name:             "nil function info returns -1",
			fn:               nil,
			expectedIdx:      -1,
			expectedWaitlist: 0,
		},
		{
			name:             "empty pods returns -1",
			fn:               &FunctionInfo{Pods: []FunctionPodInfo{}},
			expectedIdx:      -1,
			expectedWaitlist: 0,
		},
		{
			name: "single pod with no load",
			fn: &FunctionInfo{Pods: []FunctionPodInfo{
				makePodInfoWithLoad(0),
			}},
			expectedIdx:      0,
			expectedWaitlist: 0,
		},
		{
			name: "single pod with load",
			fn: &FunctionInfo{Pods: []FunctionPodInfo{
				makePodInfoWithLoad(5),
			}},
			expectedIdx:      0,
			expectedWaitlist: 5,
		},
		{
			name: "multiple pods selects least loaded",
			fn: &FunctionInfo{Pods: []FunctionPodInfo{
				makePodInfoWithLoad(5),
				makePodInfoWithLoad(1),
				makePodInfoWithLoad(8),
			}},
			expectedIdx:      1,
			expectedWaitlist: 1,
		},
		{
			name: "multiple pods with equal load selects first",
			fn: &FunctionInfo{Pods: []FunctionPodInfo{
				makePodInfoWithLoad(3),
				makePodInfoWithLoad(3),
				makePodInfoWithLoad(3),
			}},
			expectedIdx:      0,
			expectedWaitlist: 3,
		},
		{
			name: "last pod is least loaded",
			fn: &FunctionInfo{Pods: []FunctionPodInfo{
				makePodInfoWithLoad(10),
				makePodInfoWithLoad(7),
				makePodInfoWithLoad(2),
			}},
			expectedIdx:      2,
			expectedWaitlist: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, waitlist := pcm.findBestPod(tt.fn)
			assert.Equal(t, tt.expectedIdx, idx)
			assert.Equal(t, tt.expectedWaitlist, waitlist)
		})
	}
}

func makeFunctionConfig(name string, ttl time.Duration, maxWaitlistLength, maxParallelPodsPerFunction int) *configapi.FunctionConfig {
	return &configapi.FunctionConfig{
		Spec: configapi.FunctionConfigSpec{
			Image: name,
			Prefixes: []string{
				"",
				runneroptions.GHCRImagePrefix,
			},
			PodExecutor: &configapi.PodExecutorConfig{
				Tags:                    []string{""},
				TimeToLive:              metav1.Duration{Duration: ttl},
				MaxParallelExecutions:   maxParallelPodsPerFunction,
				PreferredMaxQueueLength: maxWaitlistLength,
			},
		},
	}
}

func TestGetParamsForImage(t *testing.T) {
	functionConfigStore := functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/functions")
	require.NoError(t, functionConfigStore.Store(makeFunctionConfig(
		"full-override",
		5*time.Minute,
		10,
		5,
	)))
	require.NoError(t, functionConfigStore.Store(makeFunctionConfig(
		"partial-override",
		3*time.Minute,
		0,
		0,
	)))
	require.NoError(t, functionConfigStore.Store(makeFunctionConfig(
		"zero-ttl",
		0,
		1,
		1,
	)))
	pcm := &podCacheManager{
		podTTL:                     10 * time.Minute,
		maxWaitlistLength:          2,
		maxParallelPodsPerFunction: 3,
		functionConfigMap:          functionConfigStore,
	}

	tests := []struct {
		name             string
		image            string
		expectedTTL      time.Duration
		expectedWaitlist int
		expectedMaxPods  int
	}{
		{
			name:             "full override from configMap",
			image:            "full-override",
			expectedTTL:      5 * time.Minute,
			expectedWaitlist: 10,
			expectedMaxPods:  5,
		},
		{
			name:             "partial override falls back to defaults for zero values",
			image:            "partial-override",
			expectedTTL:      3 * time.Minute,
			expectedWaitlist: 2, // default
			expectedMaxPods:  3, // default
		},
		{
			name:             "zero TTL falls back to default TTL",
			image:            "zero-ttl",
			expectedTTL:      10 * time.Minute, // default
			expectedWaitlist: 1,
			expectedMaxPods:  1,
		},
		{
			name:             "image not in configMap uses all defaults",
			image:            "unknown-image",
			expectedTTL:      10 * time.Minute,
			expectedWaitlist: 2,
			expectedMaxPods:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl, maxWaitlist, maxPods := pcm.getParamsForImage(tt.image)
			assert.Equal(t, tt.expectedTTL, ttl)
			assert.Equal(t, tt.expectedWaitlist, maxWaitlist)
			assert.Equal(t, tt.expectedMaxPods, maxPods)
		})
	}
}

func TestNewPodInfo(t *testing.T) {
	t.Run("nil channel creates pod with empty waitlist", func(t *testing.T) {
		pod := NewPodInfo(nil)
		assert.Nil(t, pod.PodData)
		assert.Empty(t, pod.Waitlist)
		assert.Equal(t, int32(0), pod.ConcurrentEvaluations.Load())
	})

	t.Run("non-nil channel adds to waitlist and increments counter", func(t *testing.T) {
		ch := make(chan *ConnectionResponse, 1)
		pod := NewPodInfo(ch)
		assert.Nil(t, pod.PodData)
		assert.Len(t, pod.Waitlist, 1)
		assert.Equal(t, int32(1), pod.ConcurrentEvaluations.Load())
	})
}

func TestSendResponse(t *testing.T) {
	t.Run("sends error when err is not nil", func(t *testing.T) {
		pod := &FunctionPodInfo{
			PodData:               &PodData{Image: "test"},
			ConcurrentEvaluations: &atomic.Int32{},
		}
		ch := make(chan *ConnectionResponse, 1)
		testErr := fmt.Errorf("test error")

		pod.SendResponse(ch, testErr)

		resp := <-ch
		assert.Error(t, resp.Err)
		assert.Equal(t, "test error", resp.Err.Error())
	})

	t.Run("sends error when podData is nil", func(t *testing.T) {
		pod := &FunctionPodInfo{
			PodData:               nil,
			ConcurrentEvaluations: &atomic.Int32{},
		}
		ch := make(chan *ConnectionResponse, 1)

		pod.SendResponse(ch, nil)

		resp := <-ch
		assert.Error(t, resp.Err)
		assert.Contains(t, resp.Err.Error(), "pod is not ready")
	})

	t.Run("sends success with podData", func(t *testing.T) {
		conn, err := grpc.NewClient("localhost:9446", grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)

		podKey := client.ObjectKey{Name: "test-pod", Namespace: "test-ns"}
		serviceKey := client.ObjectKey{Name: "test-svc", Namespace: "test-ns"}
		pod := &FunctionPodInfo{
			PodData: &PodData{
				Image:          "test-image",
				GrpcConnection: conn,
				PodKey:         &podKey,
				ServiceKey:     &serviceKey,
			},
			ConcurrentEvaluations: &atomic.Int32{},
		}
		ch := make(chan *ConnectionResponse, 1)

		pod.SendResponse(ch, nil)

		resp := <-ch
		assert.NoError(t, resp.Err)
		assert.Equal(t, "test-image", resp.PodData.Image)
		assert.NotNil(t, resp.GrpcConnection)
		assert.NotNil(t, resp.ConcurrentEvaluations)
	})
}

func TestWaitlistLen(t *testing.T) {
	counter := &atomic.Int32{}
	counter.Store(7)
	pod := FunctionPodInfo{
		ConcurrentEvaluations: counter,
	}
	assert.Equal(t, 7, pod.WaitlistLen())

	counter.Store(0)
	assert.Equal(t, 0, pod.WaitlistLen())
}

func TestFunctionInfo(t *testing.T) {
	pcm := &podCacheManager{
		functions: map[string]*FunctionInfo{},
	}

	t.Run("creates new entry for unknown image", func(t *testing.T) {
		fn := pcm.FunctionInfo("new-image")
		assert.NotNil(t, fn)
		assert.Empty(t, fn.Pods)
		// Verify it was stored in the map
		stored, ok := pcm.functions["new-image"]
		assert.True(t, ok)
		assert.Equal(t, fn, stored)
	})

	t.Run("returns existing entry for known image", func(t *testing.T) {
		existing := &FunctionInfo{Pods: []FunctionPodInfo{makePodInfoWithLoad(1)}}
		pcm.functions["existing-image"] = existing

		fn := pcm.FunctionInfo("existing-image")
		assert.Equal(t, existing, fn)
		assert.Len(t, fn.Pods, 1)
	})
}

func TestRemoveUnhealthyPods(t *testing.T) {
	const testNs = "test-ns"

	t.Run("nil function info is no-op", func(t *testing.T) {
		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		// Should not panic
		pcm.removeUnhealthyPods(nil, false)
	})

	t.Run("pod under creation (nil podData) is kept", func(t *testing.T) {
		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		fn := &FunctionInfo{
			Pods: []FunctionPodInfo{
				{
					PodData:               nil, // under creation
					ConcurrentEvaluations: &atomic.Int32{},
				},
			},
		}
		pcm.removeUnhealthyPods(fn, false)
		assert.Len(t, fn.Pods, 1, "pod under creation should be kept")
	})

	t.Run("pod not found in k8s is removed", func(t *testing.T) {
		// Pod exists in cache but NOT in fake k8s client
		podKey := client.ObjectKey{Name: "gone-pod", Namespace: testNs}
		serviceKey := client.ObjectKey{Name: "gone-svc", Namespace: testNs}
		serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
		address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
		conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(), // empty - no pods
				namespace:  testNs,
			},
		}
		fn := &FunctionInfo{
			Pods: []FunctionPodInfo{
				makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0),
			},
		}
		pcm.removeUnhealthyPods(fn, false)
		assert.Empty(t, fn.Pods, "pod not found in k8s should be removed")
	})

	t.Run("pod in Failed state is removed", func(t *testing.T) {
		podKey := client.ObjectKey{Name: "failed-pod", Namespace: testNs}
		serviceKey := client.ObjectKey{Name: "failed-svc", Namespace: testNs}
		serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
		address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
		conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "failed-pod", Namespace: testNs},
			Status:     corev1.PodStatus{Phase: corev1.PodFailed},
		}
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "failed-svc", Namespace: testNs},
		}

		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build(),
				namespace:  testNs,
			},
		}
		fn := &FunctionInfo{
			Pods: []FunctionPodInfo{
				makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0),
			},
		}
		pcm.removeUnhealthyPods(fn, false)
		assert.Empty(t, fn.Pods, "pod in Failed state should be removed")
	})

	t.Run("healthy pod is kept", func(t *testing.T) {
		podKey := client.ObjectKey{Name: "healthy-pod", Namespace: testNs}
		serviceKey := client.ObjectKey{Name: "healthy-svc", Namespace: testNs}
		serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
		address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
		conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "healthy-pod", Namespace: testNs},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "healthy-svc", Namespace: testNs},
		}

		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build(),
				namespace:  testNs,
			},
		}
		fn := &FunctionInfo{
			Pods: []FunctionPodInfo{
				makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0),
			},
		}
		pcm.removeUnhealthyPods(fn, false)
		assert.Len(t, fn.Pods, 1, "healthy pod should be kept")
	})

	t.Run("idle pod past TTL removed when removeIdle is true", func(t *testing.T) {
		podKey := client.ObjectKey{Name: "idle-pod", Namespace: testNs}
		serviceKey := client.ObjectKey{Name: "idle-svc", Namespace: testNs}
		serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
		address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
		conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "idle-pod", Namespace: testNs},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "idle-svc", Namespace: testNs},
		}

		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build(),
				namespace:  testNs,
			},
		}

		podInfo := makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0)
		podInfo.LastActivity = time.Now().Add(-15 * time.Minute) // past TTL
		fn := &FunctionInfo{Pods: []FunctionPodInfo{podInfo}}

		pcm.removeUnhealthyPods(fn, true)
		assert.Empty(t, fn.Pods, "idle pod past TTL should be removed when removeIdle=true")
	})

	t.Run("idle pod past TTL kept when removeIdle is false", func(t *testing.T) {
		podKey := client.ObjectKey{Name: "idle-pod2", Namespace: testNs}
		serviceKey := client.ObjectKey{Name: "idle-svc2", Namespace: testNs}
		serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
		address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
		conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "idle-pod2", Namespace: testNs},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "idle-svc2", Namespace: testNs},
		}

		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build(),
				namespace:  testNs,
			},
		}

		podInfo := makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0)
		podInfo.LastActivity = time.Now().Add(-15 * time.Minute) // past TTL
		fn := &FunctionInfo{Pods: []FunctionPodInfo{podInfo}}

		pcm.removeUnhealthyPods(fn, false)
		assert.Len(t, fn.Pods, 1, "idle pod past TTL should be kept when removeIdle=false")
	})
}

func TestGarbageCollectorUnit(t *testing.T) {
	const testNs = "test-ns"

	podKey := client.ObjectKey{Name: "gc-pod", Namespace: testNs}
	serviceKey := client.ObjectKey{Name: "gc-svc", Namespace: testNs}
	serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
	address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
	conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

	k8sPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gc-pod", Namespace: testNs},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	k8sSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "gc-svc", Namespace: testNs},
	}

	t.Run("removes empty function entries from map", func(t *testing.T) {
		pcm := &podCacheManager{
			podTTL:            1 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build(),
				namespace:  testNs,
			},
			functions: map[string]*FunctionInfo{
				"expired-image": {
					Pods: []FunctionPodInfo{
						func() FunctionPodInfo {
							p := makeReadyPodInfo("expired-image", podKey, serviceKey, conn, 0)
							p.LastActivity = time.Now().Add(-5 * time.Minute)
							return p
						}(),
					},
				},
			},
		}

		pcm.garbageCollector()
		// Allow background deletion goroutines to execute
		time.Sleep(100 * time.Millisecond)

		assert.Empty(t, pcm.functions, "expired function entry should be removed from map")
	})
}

func TestRedistributeLoad(t *testing.T) {
	const testNs = "test-ns"

	t.Run("redistributes connections to available pods", func(t *testing.T) {
		podKey := client.ObjectKey{Name: "redist-pod", Namespace: testNs}
		serviceKey := client.ObjectKey{Name: "redist-svc", Namespace: testNs}
		serviceUrl := serviceKey.Name + "." + serviceKey.Namespace + serviceDnsNameSuffix
		address := net.JoinHostPort(serviceUrl, defaultWrapperServerPort)
		conn, _ := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "redist-pod", Namespace: testNs},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "redist-svc", Namespace: testNs},
		}

		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build(),
				namespace:  testNs,
			},
			functions: map[string]*FunctionInfo{},
		}

		readyPod := makeReadyPodInfo("test-image", podKey, serviceKey, conn, 0)
		fn := &FunctionInfo{Pods: []FunctionPodInfo{readyPod}}
		pcm.functions["test-image"] = fn

		// Create connection channels to redistribute
		ch1 := make(chan *ConnectionResponse, 1)
		ch2 := make(chan *ConnectionResponse, 1)

		result := pcm.redistributeLoad("test-image", fn, []chan<- *ConnectionResponse{ch1, ch2})
		assert.True(t, result, "should redistribute successfully")

		// Both channels should receive responses
		resp1 := <-ch1
		assert.NoError(t, resp1.Err)
		resp2 := <-ch2
		assert.NoError(t, resp2.Err)
	})

	t.Run("returns false when no pods available", func(t *testing.T) {
		pcm := &podCacheManager{
			podTTL:            10 * time.Minute,
			functionConfigMap: functionconfigs.NewStore(runneroptions.GHCRImagePrefix, "/function"),
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
				namespace:  testNs,
			},
			functions: map[string]*FunctionInfo{},
		}
		fn := &FunctionInfo{Pods: []FunctionPodInfo{}}
		pcm.functions["empty-image"] = fn

		ch := make(chan *ConnectionResponse, 1)
		result := pcm.redistributeLoad("empty-image", fn, []chan<- *ConnectionResponse{ch})
		assert.False(t, result, "should return false with no pods")
	})
}

func TestDeletePodWithServiceInBackgroundByObjectKey(t *testing.T) {
	t.Run("deletes both pod and service", func(t *testing.T) {
		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pd-pod", Namespace: "test-ns"},
		}
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "pd-svc", Namespace: "test-ns"},
		}
		kubeClient := fake.NewClientBuilder().WithObjects(k8sPod, k8sSvc).Build()
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: kubeClient,
			},
		}

		podKey := client.ObjectKeyFromObject(k8sPod)
		serviceKey := client.ObjectKeyFromObject(k8sSvc)
		pd := PodData{
			PodKey:     &podKey,
			ServiceKey: &serviceKey,
		}

		pcm.DeletePodWithServiceInBackgroundByObjectKey(pd)
		time.Sleep(200 * time.Millisecond)

		var pod corev1.Pod
		err := kubeClient.Get(t.Context(), podKey, &pod)
		assert.Error(t, err, "pod should be deleted")

		var svc corev1.Service
		err = kubeClient.Get(t.Context(), serviceKey, &svc)
		assert.Error(t, err, "service should be deleted")
	})

	t.Run("handles nil keys gracefully", func(t *testing.T) {
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		pd := PodData{
			PodKey:     nil,
			ServiceKey: nil,
		}
		// Should not panic
		pcm.DeletePodWithServiceInBackgroundByObjectKey(pd)
		time.Sleep(50 * time.Millisecond)
	})
}

func TestDeletePodAndWait(t *testing.T) {
	t.Run("deletes pod and waits for removal", func(t *testing.T) {
		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "wait-pod", Namespace: "test-ns"},
		}
		kubeClient := fake.NewClientBuilder().WithObjects(k8sPod).Build()
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient:      kubeClient,
				namespace:       "test-ns",
				podReadyTimeout: 5 * time.Second,
			},
		}

		err := pcm.deletePodAndWait(k8sPod)
		assert.NoError(t, err)

		var pod corev1.Pod
		getErr := kubeClient.Get(t.Context(), client.ObjectKeyFromObject(k8sPod), &pod)
		assert.Error(t, getErr, "pod should be deleted")
	})
}

func TestEnsureCustomAuthSecret(t *testing.T) {
	dockerConfig := `{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`

	tmpFile, err := os.CreateTemp("", "dockerconfig-*.json")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString(dockerConfig)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	kubeClient := fake.NewClientBuilder().Build()
	pm := &podManager{
		kubeClient: kubeClient,
		namespace:  "test-ns",
	}

	err = pm.ensureCustomAuthSecret(t.Context(), tmpFile.Name(), "test-secret")
	assert.NoError(t, err)

	// Verify secret was created
	var secret corev1.Secret
	err = kubeClient.Get(t.Context(), client.ObjectKey{Name: "test-secret", Namespace: "test-ns"}, &secret)
	assert.NoError(t, err)
}

func TestDeletePodInBackground(t *testing.T) {
	t.Run("nil pod does not panic", func(t *testing.T) {
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		// Should not panic
		pcm.DeletePodInBackground(nil)
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("pod with empty name is skipped", func(t *testing.T) {
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		pod := &corev1.Pod{}
		pcm.DeletePodInBackground(pod)
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("normal pod is deleted", func(t *testing.T) {
		k8sPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "to-delete", Namespace: "test-ns"},
		}
		kubeClient := fake.NewClientBuilder().WithObjects(k8sPod).Build()
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: kubeClient,
			},
		}
		pcm.DeletePodInBackground(k8sPod)
		time.Sleep(100 * time.Millisecond)

		var pod corev1.Pod
		err := kubeClient.Get(t.Context(), client.ObjectKeyFromObject(k8sPod), &pod)
		assert.Error(t, err, "pod should be deleted")
	})
}

func TestDeleteServiceInBackground(t *testing.T) {
	t.Run("nil service does not panic", func(t *testing.T) {
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		pcm.DeleteServiceInBackground(nil)
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("service with empty name is skipped", func(t *testing.T) {
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: fake.NewClientBuilder().Build(),
			},
		}
		svc := &corev1.Service{}
		pcm.DeleteServiceInBackground(svc)
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("normal service is deleted", func(t *testing.T) {
		k8sSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "to-delete-svc", Namespace: "test-ns"},
		}
		kubeClient := fake.NewClientBuilder().WithObjects(k8sSvc).Build()
		pcm := &podCacheManager{
			podManager: &podManager{
				kubeClient: kubeClient,
			},
		}
		pcm.DeleteServiceInBackground(k8sSvc)
		time.Sleep(100 * time.Millisecond)

		var svc corev1.Service
		err := kubeClient.Get(t.Context(), client.ObjectKeyFromObject(k8sSvc), &svc)
		assert.Error(t, err, "service should be deleted")
	})
}
