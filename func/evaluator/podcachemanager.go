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

package evaluator

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kptdev/porch/controllers/functionconfigs"
	. "github.com/kptdev/porch/func/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// podCacheManager manages the cache of the pods and the corresponding GRPC clients.
// It also does the garbage collection after pods' TTL.
// It has 2 receive-only channels: connectionRequestCh and podReadyCh.
// It listens to the connectionRequestCh channel and receives clientConnRequest from the
// GRPC request handlers and add them in the waitlists.
// It also listens to the podReadyCh channel. If a pod is ready, it notifies the
// goroutines by sending back the GRPC client by lookup the waitlists mapping.
type podCacheManager struct {
	gcScanInterval time.Duration
	podTTL         time.Duration

	// connectionRequestCh receives requests for a connection to a KRM function evaluator pod
	connectionRequestCh <-chan *ConnectionRequest
	// podReadyCh is a channel to receive the information when a pod is ready.
	podReadyCh <-chan *PodReadyResponse

	// functions maps KRM function image names to its pods and waitlist information.
	functions map[string]*FunctionInfo

	podManager *podManager

	maxWaitlistLength          int
	maxParallelPodsPerFunction int
	functionConfigMap          *functionconfigs.FunctionConfigStore
}

func (pcm *podCacheManager) redistributeLoad(image string, fn *FunctionInfo, connections []chan<- *ConnectionResponse) bool {
	pcm.removeUnhealthyPods(fn, false)
	redistributed := false
	for _, ch := range connections {
		bestPodIndex, _ := pcm.findBestPod(fn)
		if bestPodIndex != -1 {
			pod := pcm.functions[image].Pods[bestPodIndex]
			if pod.PodData != nil {
				pod.SendResponse(ch, nil)
			} else {
				pod.Waitlist = append(pod.Waitlist, ch)
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
			if pcm.podManager.imageResolver != nil {
				req.Image = pcm.podManager.imageResolver(req.Image)
			}
			fn := pcm.FunctionInfo(req.Image)

			shouldScaleUp := false
			pcm.removeUnhealthyPods(fn, false)
			bestPodIndex, bestWaitlistLen := pcm.findBestPod(fn)
			_, maxWaitlist, maxPods := pcm.getParamsForImage(req.Image)
			if bestPodIndex == -1 {
				shouldScaleUp = true
			} else {
				if bestWaitlistLen >= maxWaitlist && len(fn.Pods) < maxPods {
					shouldScaleUp = true
				}
			}

			if shouldScaleUp {
				klog.Infof("Scaling up for image %s. No idle pods available. Starting a new pod.", req.Image)

				fn.Pods = append(fn.Pods, NewPodInfo(req.ResponseCh))

				config, _ := pcm.functionConfigMap.Get(req.Image)
				go pcm.podManager.getFuncEvalPodClient(context.Background(), req.Image, len(fn.Pods), config.PodExecutor, true)
			} else {
				pod := &fn.Pods[bestPodIndex]
				klog.Infof("Queuing request for %s on pod instance #%d (queue length will be %d)", req.Image, bestPodIndex, bestWaitlistLen+1)
				pod.LastActivity = time.Now()
				pod.ConcurrentEvaluations.Add(1)
				if pod.PodData != nil {
					pod.SendResponse(req.ResponseCh, nil)
				} else {
					pod.Waitlist = append(pod.Waitlist, req.ResponseCh)
				}
			}

		case podReadyMsg := <-pcm.podReadyCh:
			if podReadyMsg.Image == "" {
				klog.Error("Received a 'pod ready' message with an empty KRM image name. This indicates a logical error in the code.")
				continue
			}
			fn, ok := pcm.functions[podReadyMsg.Image]
			if !ok {
				klog.Errorf("Received a ready pod for %q, but the KRM function is missing from the pool! Ignoring.", podReadyMsg.Image)
				continue
			}
			// Find the first pod with nil podData, which means it is pending creation.
			toUpdate := slices.IndexFunc(fn.Pods, func(pod FunctionPodInfo) bool {
				return pod.PodData == nil
			})
			if toUpdate == -1 {
				klog.Errorf("Received a ready pod for %q, but no pending instance was found in the pod pool. Total of %d pods was in the pool. Ignoring.", podReadyMsg.Image, len(fn.Pods))
				continue
			}

			if podReadyMsg.Err != nil {
				klog.Warningf("Pod creation failed for image %s: %v", podReadyMsg.Image, podReadyMsg.Err)
				waitListToRedistribute := fn.Pods[toUpdate].Waitlist
				failedPod := fn.Pods[toUpdate]
				fn.Pods = slices.Delete(fn.Pods, toUpdate, toUpdate+1)
				redistributed := false
				if len(fn.Pods) > 0 {
					redistributed = pcm.redistributeLoad(podReadyMsg.Image, fn, waitListToRedistribute)
				}
				if !redistributed {
					for _, ch := range waitListToRedistribute {
						failedPod.SendResponse(ch, podReadyMsg.Err)
					}
				}
				pcm.DeletePodWithServiceInBackgroundByObjectKey(podReadyMsg.PodData)
				continue
			}

			pod := &fn.Pods[toUpdate]
			pod.PodData = &podReadyMsg.PodData
			pod.LastActivity = time.Now()
			klog.Infof("New pod %s is ready for image %s. Total number of pods for image: %d", podReadyMsg.PodKey.Name, podReadyMsg.Image, len(fn.Pods))
			for _, ch := range pod.Waitlist {
				pod.SendResponse(ch, nil)
			}
			pod.Waitlist = nil

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
	if entry, ok := pcm.functionConfigMap.Get(image); ok && entry.PodExecutor != nil {
		podExecutorConfig := entry.PodExecutor
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

func (pcm *podCacheManager) FunctionInfo(image string) *FunctionInfo {
	fn, ok := pcm.functions[image]
	if !ok {
		fn = &FunctionInfo{}
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
					if len(fn.Pods) < pcm.maxParallelPodsPerFunction && pod.Status.Phase == corev1.PodRunning {
						pData, err := pcm.podManager.createPodData(ctx, serviceKey, podKey, image)
						if err == nil {
							klog.Infof("retrieved function evaluator pod %s/%s for %s", pod.Namespace, pod.Name, image)
							fn.Pods = append(fn.Pods, NewPodInfo(nil))
							pcm.podManager.podReadyCh <- &PodReadyResponse{
								PodData: *pData,
								Err:     nil,
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
	for spec := range pcm.functionConfigMap.IterPodConfigSpecs() {
		if spec.PodExecutor != nil {
			image := spec.Image
			if len(spec.PodExecutor.Tags) > 0 && len(spec.PodExecutor.Tags[0]) > 0 {
				image += ":" + spec.PodExecutor.Tags[0]
			} else {
				image += ":latest"
			}
			if len(spec.Prefixes) > 0 && spec.Prefixes[0] != "" {
				image = ImageJoin(spec.Prefixes[0], image)
			} else {
				image = ImageJoin(defaultImagePrefix, image)
			}
			image = pcm.podManager.imageResolver(image)
			fn := pcm.FunctionInfo(image)
			if len(fn.Pods) == 0 {
				fn.Pods = append(fn.Pods, NewPodInfo(nil))
				go func(fnImage string) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					pcm.podManager.getFuncEvalPodClient(ctx, fnImage, 1, spec.PodExecutor, false)
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
func (pcm *podCacheManager) findBestPod(fn *FunctionInfo) (int, int) {
	if fn == nil {
		return -1, 0
	}
	n := len(fn.Pods)
	if n == 0 {
		return -1, 0
	}

	minWaitlist := 0
	// Find the minimum waitlist length across all pods
	minWaitlist = fn.Pods[0].WaitlistLen()
	for i := 1; i < n; i++ {
		wl := fn.Pods[i].WaitlistLen()
		if wl < minWaitlist {
			minWaitlist = wl
		}
	}

	// Round-robin among pods that have the minimum waitlist length
	for i := 0; i < n; i++ {
		idx := (fn.RoundRobinIdx + i) % n
		if fn.Pods[idx].WaitlistLen() == minWaitlist {
			fn.RoundRobinIdx = (idx + 1) % n
			return idx, minWaitlist
		}
	}

	// This should never happen since minWaitlist was calculated from these same pods
	return -1, 0
}

// removeUnhealthyPods removes unhealthy pods from the function's pod list.
// If removeIdle is true, it will also remove idle pods that have reached their TTL.
func (pcm *podCacheManager) removeUnhealthyPods(fn *FunctionInfo, removeIdle bool) {
	if fn == nil {
		return
	}
	fn.Pods = slices.DeleteFunc(fn.Pods, func(pod FunctionPodInfo) bool {
		removeFromCache := false
		if pod.PodData == nil {
			// pod is under creation
			return false
		}

		k8sPod := &corev1.Pod{}
		err := pcm.podManager.kubeClient.Get(context.Background(), *pod.PodKey, k8sPod)
		if err != nil {
			if apierrors.IsNotFound(err) {
				klog.Infof("Removing deleted pod from cache for image %s", pod.Image)
			} else {
				klog.Errorf("Failed to get pod %v, removing from cache: %v", pod.PodKey, err)
			}
			removeFromCache = true
		}

		service := &corev1.Service{}
		err = pcm.podManager.kubeClient.Get(context.Background(), *pod.ServiceKey, service)
		if err != nil {
			if apierrors.IsNotFound(err) {
				klog.Infof("Removing deleted service from cache for image %s", pod.Image)
			} else {
				klog.Errorf("Failed to get service %v, removing from cache: %v", pod.ServiceKey, err)
			}
			removeFromCache = true
		}

		err = pcm.podManager.kubeClient.Get(context.Background(), *pod.ServiceKey, service)
		if err != nil {
			klog.Warningf("unable to find expected service %s namespace %s: %v", pod.ServiceKey.Name, k8sPod.Namespace, err)
		}

		if k8sPod.Status.Phase == corev1.PodFailed {
			klog.Errorf("Evicting pod in failed state (%s/%s) from cache for image %s", k8sPod.Namespace, k8sPod.Name, pod.Image)
			removeFromCache = true
		}

		serviceUrl := service.Name + "." + service.Namespace + serviceDnsNameSuffix
		if net.JoinHostPort(serviceUrl, defaultWrapperServerPort) != pod.GrpcConnection.Target() {
			klog.Errorf("Evicting pod whose pod IP doesn't match with its grpc connection (%s/%s) from cache for image %s", k8sPod.Namespace, k8sPod.Name, pod.Image)
			removeFromCache = true
		}
		ttl, _, _ := pcm.getParamsForImage(pod.Image)
		if removeIdle && pod.WaitlistLen() == 0 && time.Since(pod.LastActivity) > ttl {
			klog.Infof("Removing idle pod %q that reached its TTL from cache for image %s", k8sPod.Name, pod.Image)
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
		if len(fn.Pods) == 0 {
			delete(pcm.functions, image)
		}
	}
}

func (pcm *podCacheManager) DeletePodWithServiceInBackgroundByObjectKey(podData PodData) {
	k8sPod := &corev1.Pod{}
	if podData.PodKey != nil {
		err := pcm.podManager.kubeClient.Get(context.Background(), *podData.PodKey, k8sPod)
		if err != nil {
			klog.Warningf("unable to find pod %s in namespace: %s: %v", podData.PodKey.Name, podData.PodKey.Namespace, err)
		}
		pcm.DeletePodInBackground(k8sPod)
	}

	service := &corev1.Service{}
	if podData.ServiceKey != nil {
		err := pcm.podManager.kubeClient.Get(context.Background(), *podData.ServiceKey, service)
		if err != nil {
			klog.Warningf("unable to find service %s in namespace %s: %v", podData.ServiceKey.Name, podData.ServiceKey.Namespace, err)
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

func NewPodInfo(firstResponseCh chan<- *ConnectionResponse) FunctionPodInfo {
	pod := FunctionPodInfo{
		Waitlist:              []chan<- *ConnectionResponse{},
		PodData:               nil, // This will be filled in when the pod is ready.
		LastActivity:          time.Now(),
		ConcurrentEvaluations: &atomic.Int32{},
	}
	if firstResponseCh != nil {
		pod.Waitlist = append(pod.Waitlist, firstResponseCh)
		pod.ConcurrentEvaluations.Add(1)
	}
	return pod
}
