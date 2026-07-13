// Copyright 2025 The kpt Authors
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

package internal

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	fnconf "github.com/kptdev/porch/controllers/functionconfigs/reconciler"
	"github.com/kptdev/porch/pkg/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// podCacheManager manages the cache of the pods and the corresponding GRPC clients.
// It also does the garbage collection after pods' TTL.
// It has 3 receive-only channels: connectionRequestCh, podReadyCh, and evictionCh.
// It listens to the connectionRequestCh channel and receives clientConnRequest from the
// GRPC request handlers and add them in the waitlists.
// It also listens to the podReadyCh channel. If a pod is ready, it notifies the
// goroutines by sending back the GRPC client by lookup the waitlists mapping.
type podCacheManager struct {
	gcScanInterval time.Duration
	podTTL         time.Duration

	// connectionRequestCh receives requests for a connection to a KRM function evaluator pod
	connectionRequestCh <-chan *connectionRequest
	// podReadyCh is a channel to receive the information when a pod is ready.
	podReadyCh <-chan *podReadyResponse
	// evictionCh receives requests to remove specific dead pods from cache
	evictionCh <-chan *podEvictionRequest

	// functions maps KRM function image names to its pods and waitlist information.
	functions map[string]*functionInfo

	podManager *podManager

	maxWaitlistLength          int
	maxParallelPodsPerFunction int
	functionConfigMap          *fnconf.FunctionConfigStore
}

// podEvictionRequest is sent after an Unavailable gRPC error to remove a dead pod from cache.
type podEvictionRequest struct {
	image  string
	podKey client.ObjectKey
	// doneCh is closed by the cache manager once the pod has been removed from cache,
	// allowing the caller to wait for eviction completion before retrying.
	doneCh chan struct{}
}

// functionInfo holds the list of all pod instances for the same KRM function image.
type functionInfo struct {
	// status of all pods belonging to the same KRM function image
	pods []functionPodInfo
	// roundRobinIdx is used to distribute requests across pods when all have equal load
	roundRobinIdx int
}

// functionPodInfo represents the state of a single pod instance.
type functionPodInfo struct {
	// podData contains the information about the pod, returned by the podManager
	// It is nil until the pod is actually started
	*podData
	// waitlist is used to temporarily store connection requests until the pod is started
	waitlist []chan<- *connectionResponse
	// time of last function evaluation, used by the garbage collector to identify idle pods
	lastActivity time.Time
	// the number of currently ongoing and waiting fn evaluations in the pod
	concurrentEvaluations *atomic.Int32
}

func (pcm *podCacheManager) redistributeLoad(image string, fn *functionInfo, connections []chan<- *connectionResponse) bool {
	pcm.removeUnhealthyPods(fn, false)
	redistributed := false
	for _, ch := range connections {
		bestPodIndex, _ := pcm.findBestPod(fn)
		if bestPodIndex != -1 {
			pod := &pcm.functions[image].pods[bestPodIndex]
			pod.concurrentEvaluations.Add(1)
			if pod.podData != nil {
				pod.SendResponse(ch, nil)
			} else {
				pod.waitlist = append(pod.waitlist, ch)
			}
			redistributed = true
		}
	}
	return redistributed
}

// podCacheManager responds to the requestCh and the podReadyCh and does the
// garbage collection synchronously.
// We must run this method in one single goroutine. Doing it this way simplify
// design around concurrency.
func (pcm *podCacheManager) podCacheManager(ctx context.Context) {
	//nolint:staticcheck
	tick := time.Tick(pcm.gcScanInterval)
	for {
		select {
		case req := <-pcm.connectionRequestCh:
			fn := pcm.FunctionInfo(req.image)

			shouldScaleUp := false
			bestPodIndex, bestWaitlistLen := pcm.findBestPod(fn)
			_, maxWaitlist, maxPods := pcm.getParamsForImage(req.image)
			if bestPodIndex == -1 {
				shouldScaleUp = true
			} else {
				if bestWaitlistLen >= maxWaitlist && len(fn.pods) < maxPods {
					shouldScaleUp = true
				}
			}

			if shouldScaleUp {
				klog.Infof("Scaling up for image %s. No idle pods available. Starting a new pod.", req.image)

				fn.pods = append(fn.pods, NewPodInfo(req.responseCh))

				functionConfig, exists := pcm.functionConfigMap.GetFunctionConfig(util.GetImageName(req.image))
				if !exists {
					functionConfig = &configapi.FunctionConfig{}
				}

				go pcm.podManager.getFuncEvalPodClient(context.Background(), req.image, len(fn.pods), functionConfig.Spec.PodExecutor, true)
			} else {
				pod := &fn.pods[bestPodIndex]
				klog.Infof("Queuing request for %s on pod instance #%d (queue length will be %d)", req.image, bestPodIndex, bestWaitlistLen+1)
				pod.lastActivity = time.Now()
				pod.concurrentEvaluations.Add(1)
				if pod.podData != nil {
					pod.SendResponse(req.responseCh, nil)
				} else {
					pod.waitlist = append(pod.waitlist, req.responseCh)
				}
			}

		case podReadyMsg := <-pcm.podReadyCh:
			if podReadyMsg.image == "" {
				klog.Error("Received a 'pod ready' message with an empty KRM image name. This indicates a logical error in the code.")
				continue
			}
			fn, ok := pcm.functions[podReadyMsg.image]
			if !ok {
				klog.Errorf("Received a ready pod for %q, but the KRM function is missing from the pool! Ignoring.", podReadyMsg.image)
				continue
			}
			// Find the first pod with nil podData, which means it is pending creation.
			toUpdate := slices.IndexFunc(fn.pods, func(pod functionPodInfo) bool {
				return pod.podData == nil
			})
			if toUpdate == -1 {
				klog.Errorf("Received a ready pod for %q, but no pending instance was found in the pod pool. Total of %d pods was in the pool. Ignoring.", podReadyMsg.image, len(fn.pods))
				continue
			}

			if podReadyMsg.err != nil {
				klog.Warningf("Pod creation failed for image %s: %v", podReadyMsg.image, podReadyMsg.err)
				waitListToRedistribute := fn.pods[toUpdate].waitlist
				failedPod := fn.pods[toUpdate]
				fn.pods = slices.Delete(fn.pods, toUpdate, toUpdate+1)
				redistributed := false
				if len(fn.pods) > 0 {
					redistributed = pcm.redistributeLoad(podReadyMsg.image, fn, waitListToRedistribute)
				}
				if !redistributed {
					for _, ch := range waitListToRedistribute {
						failedPod.SendResponse(ch, podReadyMsg.err)
					}
				}
				pcm.DeletePodWithServiceInBackgroundByObjectKey(podReadyMsg.podData)
				continue
			}

			pod := &fn.pods[toUpdate]
			pod.podData = &podReadyMsg.podData
			pod.lastActivity = time.Now()
			klog.Infof("New pod %s is ready for image %s. Total number of pods for image: %d", podReadyMsg.podKey.Name, podReadyMsg.image, len(fn.pods))
			for _, ch := range pod.waitlist {
				pod.SendResponse(ch, nil)
			}
			pod.waitlist = nil

		case evict := <-pcm.evictionCh:
			fn, ok := pcm.functions[evict.image]
			if !ok {
				if evict.doneCh != nil {
					close(evict.doneCh)
				}
				continue
			}
			idx := slices.IndexFunc(fn.pods, func(pod functionPodInfo) bool {
				return pod.podData != nil && pod.podKey != nil && *pod.podKey == evict.podKey
			})
			if idx != -1 {
				// Check if the pod still exists and is healthy in k8s.
				// Use a bounded context to avoid blocking the event loop on API-server issues.
				k8sPod := &corev1.Pod{}
				getCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				err := pcm.podManager.kubeClient.Get(getCtx, *fn.pods[idx].podKey, k8sPod)
				cancel()
				if apierrors.IsNotFound(err) {
					klog.Infof("Evicting missing pod %s from cache for image %s (Unavailable)", evict.podKey.Name, evict.image)
					if fn.pods[idx].grpcConnection != nil {
						fn.pods[idx].grpcConnection.Close()
					}
					fn.pods = slices.Delete(fn.pods, idx, idx+1)
				} else if err != nil {
					// Transient API error — keep the pod in cache rather than evicting a healthy pod.
					klog.Warningf("Failed to confirm pod health for %s/%s; keeping it in cache: %v", evict.podKey.Namespace, evict.podKey.Name, err)
				} else if k8sPod.Status.Phase != corev1.PodRunning || k8sPod.DeletionTimestamp != nil {
					klog.Infof("Evicting dead pod %s from cache for image %s (Unavailable)", evict.podKey.Name, evict.image)
					if fn.pods[idx].grpcConnection != nil {
						fn.pods[idx].grpcConnection.Close()
					}
					pcm.DeletePodInBackground(k8sPod)
					fn.pods = slices.Delete(fn.pods, idx, idx+1)
				}
			}
			if evict.doneCh != nil {
				close(evict.doneCh)
			}

		case <-tick:
			pcm.garbageCollector()
		case <-ctx.Done():
			klog.Info("Pod cache manager shut down")
			return
		}

	}
}

// getParamsForImage returns the pod cache parameters (TTL, maxWaitlist, maxPods) for the given function image.
// If the image is present in the configMap, it returns the specific parameters for that image.
// Otherwise, it falls back to the global defaults (pcm.podTTL, pcm.maxWaitlistLength, pcm.maxParallelPodsPerFunction).
func (pcm *podCacheManager) getParamsForImage(image string) (ttl time.Duration, maxWaitlist, maxPods int) {
	if entry, ok := pcm.functionConfigMap.GetFunctionConfig(util.GetImageName(image)); ok && entry.Spec.PodExecutor != nil {
		podExecutorConfig := entry.Spec.PodExecutor
		parsedTTL := podExecutorConfig.TimeToLive.Duration
		if parsedTTL <= 0 {
			parsedTTL = pcm.podTTL
		}
		maxWaitlist := podExecutorConfig.PreferredMaxQueueLength
		if maxWaitlist == 0 {
			maxWaitlist = pcm.maxWaitlistLength
		}
		maxPods := podExecutorConfig.MaxParallelExecutions
		if maxPods == 0 {
			maxPods = pcm.maxParallelPodsPerFunction
		}
		return parsedTTL, maxWaitlist, maxPods
	}
	return pcm.podTTL, pcm.maxWaitlistLength, pcm.maxParallelPodsPerFunction
}

func (pcm *podCacheManager) FunctionInfo(image string) *functionInfo {
	fn, ok := pcm.functions[image]
	if !ok {
		fn = &functionInfo{}
		pcm.functions[image] = fn
	}
	return fn
}

func (pcm *podCacheManager) retrieveFunctionPods(ctx context.Context) error {
	template, err := pcm.podManager.getBasePodTemplate(ctx)
	if err != nil {
		klog.Errorf("failed to generate a base pod template: %v", err)
		return fmt.Errorf("failed to generate a base pod template: %w", err)
	}

	podList := &corev1.PodList{}
	err = pcm.podManager.kubeClient.List(ctx, podList, client.InNamespace(pcm.podManager.namespace), client.HasLabels{krmFunctionImageLabel})
	if err != nil {
		klog.Warningf("error when listing pods in namespace: %q: %v", pcm.podManager.namespace, err)
	}
	if err == nil && len(podList.Items) > 0 {
		for _, pod := range podList.Items {
			if pod.DeletionTimestamp == nil {
				if isPodTemplateSameVersion(&pod, template.ResourceVersion) {
					// Service name is Image Label set on Pod manifest
					serviceName := pod.Labels[krmFunctionImageLabel]
					podKey := client.ObjectKeyFromObject(&pod)

					serviceTemplate, err := pcm.podManager.retrieveOrCreateService(ctx, serviceName)
					if err != nil {
						return err
					}
					serviceKey := client.ObjectKeyFromObject(serviceTemplate)

					//nolint:staticcheck
					var endpoint corev1.Endpoints
					if err := pcm.podManager.kubeClient.Get(ctx, serviceKey, &endpoint); err != nil {
						return err
					}
					// Remove the pod if more than one address is found in the endpoint
					if len(endpoint.Subsets[0].Addresses) > 1 {
						err = pcm.deletePodAndWait(&pod)
						if err != nil {
							klog.Errorf("failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
						}
						continue
					}

					image := pod.Spec.Containers[0].Image
					fn := pcm.FunctionInfo(image)
					if len(fn.pods) < pcm.maxParallelPodsPerFunction && pod.Status.Phase == corev1.PodRunning {
						pData, err := pcm.podManager.createPodData(ctx, serviceKey, podKey, image)
						if err == nil {
							// Verify gRPC is reachable before adding to cache
							if !pcm.podManager.skipGrpcReadyCheck {
								if grpcErr := pcm.podManager.waitForGrpcReady(ctx, pData.grpcConnection); grpcErr != nil {
									klog.Warningf("retrieved pod %s/%s for %s but gRPC not ready, deleting: %v", pod.Namespace, pod.Name, image, grpcErr)
									pData.grpcConnection.Close()
									pcm.DeletePodInBackground(&pod)
									continue
								}
							}
							klog.Infof("retrieved function evaluator pod %s/%s for %s", pod.Namespace, pod.Name, image)
							fn.pods = append(fn.pods, NewPodInfo(nil))
							pcm.podManager.podReadyCh <- &podReadyResponse{
								podData: *pData,
								err:     nil,
							}
							continue
						}
					}

					klog.Infof("Max parallel pods reached for %q, deleting %s/%s", image, pod.Namespace, pod.Name)
					pcm.DeletePodInBackground(&pod)
					pcm.DeleteServiceInBackground(serviceTemplate)
				}
			}
		}
	}
	return nil
}

// warmupCache starts preloading 1 pod in the background for each function specified in podCacheConfig
func (pcm *podCacheManager) warmupCache(defaultImagePrefix string) error {
	start := time.Now()
	defer func() {
		klog.Infof("cache warming is completed and it took %v", time.Since(start))
	}()
	for _, entry := range pcm.functionConfigMap.List() {
		if entry.Spec.PodExecutor != nil && len(entry.Spec.PodExecutor.Tags) > 0 {
			image := entry.Spec.Image
			if len(entry.Spec.PodExecutor.Tags[0]) > 0 {
				image = fmt.Sprintf("%s:%s", entry.Spec.Image, entry.Spec.PodExecutor.Tags[0])
			}
			if len(entry.Spec.Prefixes) > 0 && entry.Spec.Prefixes[0] != "" {
				image = ImageJoin(entry.Spec.Prefixes[0], image)
			} else {
				image = ImageJoin(defaultImagePrefix, image)
			}
			fn := pcm.FunctionInfo(image)
			if len(fn.pods) == 0 {
				fn.pods = append(fn.pods, NewPodInfo(nil))
				go func(fnImage string) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					functionConfig, exists := pcm.functionConfigMap.GetFunctionConfig(entry.Spec.Image)
					if !exists {
						functionConfig = &configapi.FunctionConfig{}
					}
					pcm.podManager.getFuncEvalPodClient(ctx, fnImage, 1, functionConfig.Spec.PodExecutor, false)
				}(image)
			}
		}
	}
	return nil
}

func ImageJoin(prefix, image string) string {
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(image, "/")
}

// findBestPod returns with the index of the best pod for the given function.
// It uses round-robin among pods with equal load to ensure even distribution.
// If there are no suitable pods, it returns with -1.
func (pcm *podCacheManager) findBestPod(fn *functionInfo) (int, int) {
	if fn == nil {
		return -1, 0
	}
	n := len(fn.pods)
	if n == 0 {
		return -1, 0
	}

	minWaitlist := 0
	// Find the minimum waitlist length across all pods
	minWaitlist = fn.pods[0].WaitlistLen()
	for i := 1; i < n; i++ {
		wl := fn.pods[i].WaitlistLen()
		if wl < minWaitlist {
			minWaitlist = wl
		}
	}

	// Round-robin among pods that have the minimum waitlist length
	for i := 0; i < n; i++ {
		idx := (fn.roundRobinIdx + i) % n
		if fn.pods[idx].WaitlistLen() == minWaitlist {
			fn.roundRobinIdx = (idx + 1) % n
			return idx, minWaitlist
		}
	}

	// This should never happen since minWaitlist was calculated from these same pods
	return -1, 0
}

// removeUnhealthyPods removes unhealthy pods from the function's pod list.
// If removeIdle is true, it will also remove idle pods that have reached their TTL.
func (pcm *podCacheManager) removeUnhealthyPods(fn *functionInfo, removeIdle bool) {
	if fn == nil {
		return
	}
	fn.pods = slices.DeleteFunc(fn.pods, func(pod functionPodInfo) bool {
		removeFromCache := false
		if pod.podData == nil {
			// pod is under creation
			return false
		}

		k8sPod := &corev1.Pod{}
		err := pcm.podManager.kubeClient.Get(context.Background(), *pod.podKey, k8sPod)
		if err != nil {
			if apierrors.IsNotFound(err) {
				klog.Infof("Removing deleted pod from cache for image %s", pod.image)
			} else {
				klog.Errorf("Failed to get pod %v, removing from cache: %v", pod.podKey, err)
			}
			removeFromCache = true
		}

		service := &corev1.Service{}
		err = pcm.podManager.kubeClient.Get(context.Background(), *pod.serviceKey, service)
		if err != nil {
			if apierrors.IsNotFound(err) {
				klog.Infof("Removing deleted service from cache for image %s", pod.image)
			} else {
				klog.Errorf("Failed to get service %v, removing from cache: %v", pod.serviceKey, err)
			}
			removeFromCache = true
		}

		err = pcm.podManager.kubeClient.Get(context.Background(), *pod.serviceKey, service)
		if err != nil {
			klog.Warningf("unable to find expected service %s namespace %s: %v", pod.serviceKey.Name, k8sPod.Namespace, err)
		}

		if k8sPod.Status.Phase == corev1.PodFailed {
			klog.Errorf("Evicting pod in failed state (%s/%s) from cache for image %s", k8sPod.Namespace, k8sPod.Name, pod.image)
			removeFromCache = true
		}

		serviceUrl := service.Name + "." + service.Namespace + serviceDnsNameSuffix
		if net.JoinHostPort(serviceUrl, defaultWrapperServerPort) != pod.grpcConnection.Target() {
			klog.Errorf("Evicting pod whose pod IP doesn't match with its grpc connection (%s/%s) from cache for image %s", k8sPod.Namespace, k8sPod.Name, pod.image)
			removeFromCache = true
		}
		ttl, _, _ := pcm.getParamsForImage(pod.image)
		if removeIdle && pod.WaitlistLen() == 0 && time.Since(pod.lastActivity) > ttl {
			klog.Infof("Removing idle pod %q that reached its TTL from cache for image %s", k8sPod.Name, pod.image)
			removeFromCache = true
		}

		if removeFromCache {
			pcm.DeletePodInBackground(k8sPod)
			pcm.DeleteServiceInBackground(service)
		}

		return removeFromCache
	})
}

// garbageCollector runs periodically and removes unhealthy and idle pods from the pool.
// TODO: We can use Watch + periodically reconciliation to manage the pods,
// the pod evaluator will become a controller.
func (pcm *podCacheManager) garbageCollector() {
	// Process each image's pods
	for image, fn := range pcm.functions {
		pcm.removeUnhealthyPods(fn, true)

		// Clean up empty slices
		if len(fn.pods) == 0 {
			delete(pcm.functions, image)
		}
	}
}

func (pcm *podCacheManager) DeletePodWithServiceInBackgroundByObjectKey(podData podData) {
	k8sPod := &corev1.Pod{}
	if podData.podKey != nil {
		err := pcm.podManager.kubeClient.Get(context.Background(), *podData.podKey, k8sPod)
		if err != nil {
			klog.Warningf("unable to find pod %s in namespace: %s: %v", podData.podKey.Name, podData.podKey.Namespace, err)
		}
		pcm.DeletePodInBackground(k8sPod)
	}

	service := &corev1.Service{}
	if podData.serviceKey != nil {
		err := pcm.podManager.kubeClient.Get(context.Background(), *podData.serviceKey, service)
		if err != nil {
			klog.Warningf("unable to find service %s in namespace %s: %v", podData.serviceKey.Name, podData.serviceKey.Namespace, err)
		}
		pcm.DeleteServiceInBackground(service)
	}
}

func (pcm *podCacheManager) deletePodAndWait(k8sPod *corev1.Pod) error {
	err := pcm.podManager.kubeClient.Delete(context.Background(), k8sPod)
	if err != nil {
		klog.Errorf("Failed to delete pod %s/%s from cluster: %v", k8sPod.Namespace, k8sPod.Name, err)
	}

	if e := wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, pcm.podManager.podReadyTimeout, true, func(ctx context.Context) (done bool, err error) {
		var current corev1.Pod
		err = pcm.podManager.kubeClient.Get(context.Background(), client.ObjectKeyFromObject(k8sPod), &current)
		if apierrors.IsNotFound(err) {
			return true, nil
		} else if err != nil {
			return false, fmt.Errorf("error while waiting for deletion: %w", err)
		}
		return false, nil
	}); e != nil {
		return fmt.Errorf("error occurred when waiting the deletion of pod. If the error is caused by timeout, you may want to examine the pod in namespace %q. Error: %w", pcm.podManager.namespace, e)
	}
	return nil
}

func (pcm *podCacheManager) DeletePodInBackground(k8sPod *corev1.Pod) {
	go func() {
		if k8sPod != nil && k8sPod.DeletionTimestamp.IsZero() && k8sPod.Name != "" {
			err := pcm.podManager.kubeClient.Delete(context.Background(), k8sPod)
			if err != nil {
				klog.Errorf("Failed to delete pod %s/%s from cluster: %v", k8sPod.Namespace, k8sPod.Name, err)
			}
		}
	}()
}

func (pcm *podCacheManager) DeleteServiceInBackground(svc *corev1.Service) {
	go func() {
		if svc != nil && svc.DeletionTimestamp.IsZero() && svc.Name != "" {
			err := pcm.podManager.kubeClient.Delete(context.Background(), svc)
			if err != nil {
				klog.Warningf("unable to delete service %s/%s: %v", svc.Namespace, svc.Name, err)
			}
		}
	}()
}

func NewPodInfo(firstResponseCh chan<- *connectionResponse) functionPodInfo {
	pod := functionPodInfo{
		waitlist:              []chan<- *connectionResponse{},
		podData:               nil, // This will be filled in when the pod is ready.
		lastActivity:          time.Now(),
		concurrentEvaluations: &atomic.Int32{},
	}
	if firstResponseCh != nil {
		pod.waitlist = append(pod.waitlist, firstResponseCh)
		pod.concurrentEvaluations.Add(1)
	}
	return pod
}

// SendResponse sends a reply to the connection request containing the pod data.
// If err != nil it sends `err` as an error response.
// It sends and error response if the pod is not ready yet (this shouldn't happen).
func (pod *functionPodInfo) SendResponse(responseCh chan<- *connectionResponse, err error) {
	switch {
	case err != nil:
		responseCh <- &connectionResponse{
			err: err,
		}
	case pod.podData == nil:
		responseCh <- &connectionResponse{
			err: fmt.Errorf("pod is not ready, connection response sent prematurely. This is logical error in the code"),
		}
	default:
		responseCh <- &connectionResponse{
			podData:               *pod.podData,
			concurrentEvaluations: pod.concurrentEvaluations,
			err:                   nil,
		}
	}
}

// WaitlistLen returns with the number of fn evaluations currently handled by the pod
func (pod functionPodInfo) WaitlistLen() int {
	return int(pod.concurrentEvaluations.Load())
}
