// Copyright 2026 The kpt and Nephio Authors
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

package reconciler

import (
	"context"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/krm-functions-catalog/functions/go/apply-replacements/replacements"
	setNamespace "github.com/kptdev/krm-functions-catalog/functions/go/set-namespace/transformer"
	"github.com/kptdev/krm-functions-catalog/functions/go/starlark/starlark"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	"github.com/nephio-project/porch/pkg/util"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const BaseFinalizer = "config.porch.kpt.dev/functionconfig"
const ServerFinalizer = BaseFinalizer + "-porch-server"
const FunctionRunnerFinalizer = BaseFinalizer + "-function-runner"
const ControllerFinalizer = BaseFinalizer + "-controller"

type BinaryCacheEntry struct {
	PrefixRegex string
	Tags        map[string]string
}

type FunctionConfigStore struct {
	mu sync.RWMutex

	functionConfigurations map[string]*configapi.FunctionConfig
	binaryExecutorCache    map[string]BinaryCacheEntry
	builtInExecutorCache   map[string]fnsdk.ResourceListProcessor

	defaultImagePrefix string
	defaultBinaryDir   string
}

func NewFunctionConfigStore(defaultImagePrefix, defaultBinaryDir string) *FunctionConfigStore {
	return &FunctionConfigStore{
		functionConfigurations: make(map[string]*configapi.FunctionConfig),
		binaryExecutorCache:    make(map[string]BinaryCacheEntry),
		builtInExecutorCache:   make(map[string]fnsdk.ResourceListProcessor),
		defaultImagePrefix:     strings.TrimRight(defaultImagePrefix, "/"),
		defaultBinaryDir:       strings.TrimRight(defaultBinaryDir, "/"),
	}
}

func (s *FunctionConfigStore) UpsertFunctionConfig(name string, obj *configapi.FunctionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.functionConfigurations[name] = obj
}

func (s *FunctionConfigStore) generateRegexPattern(prefixes []string, imageName string) string {
	var preparedPrefixes []string
	for _, prefix := range prefixes {
		if prefix == "" {
			preparedPrefixes = append(preparedPrefixes, regexp.QuoteMeta(s.defaultImagePrefix))
		} else {
			preparedPrefixes = append(preparedPrefixes, regexp.QuoteMeta(prefix))
		}
	}

	return "^(?:" + strings.Join(preparedPrefixes, "|") + ")"

}

func splitImage(image string) (name string, tag string) {
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")

	if lastColon > lastSlash {
		return image[:lastColon], image[lastColon+1:]
	}
	return image, ""
}

func (s *FunctionConfigStore) UpdateBinaryCache(_ string, obj *configapi.FunctionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var binaryCacheEntry BinaryCacheEntry
	binaryCacheEntry.Tags = make(map[string]string)
	// Create a prefix Regex
	binaryCacheEntry.PrefixRegex = s.generateRegexPattern(obj.Spec.Prefixes, obj.Spec.Image)

	abs := obj.Spec.BinaryExecutor.Path
	if abs[0] != '/' {
		var err error
		abs, err = filepath.Abs(filepath.Join(s.defaultBinaryDir, obj.Spec.BinaryExecutor.Path))
		if err != nil {
			klog.Warningf("Failed to cache %q: %v", obj.Spec.Image, err)
			return
		}
	}

	for _, tag := range obj.Spec.BinaryExecutor.Tags {
		binaryCacheEntry.Tags[tag] = abs
	}
	s.binaryExecutorCache[obj.Spec.Image] = binaryCacheEntry
}

func (s *FunctionConfigStore) UpdateExecCache(name string, functionConfig *configapi.FunctionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	functionAliases := map[string][]string{}

	id := name
	if functionConfig.Spec.GoExecutor.ID != nil {
		id = *functionConfig.Spec.GoExecutor.ID
	}
	for _, tag := range functionConfig.Spec.GoExecutor.Tags {
		image := id + ":" + tag
		functionAliases[name] = append(functionAliases[name], image)
	}

	applyMappings := func(aliases []string, fn fnsdk.ResourceListProcessorFunc) {
		//Clear previous entries for the actual function
		for img := range s.builtInExecutorCache {
			if strings.Contains(img, name) {
				delete(s.builtInExecutorCache, img)
			}
		}
		for _, img := range aliases {
			s.builtInExecutorCache[img] = fn
			s.builtInExecutorCache[runneroptions.GHCRImagePrefix+img] = fn
			if s.defaultImagePrefix != "" && s.defaultImagePrefix != runneroptions.GHCRImagePrefix {
				s.builtInExecutorCache[s.defaultImagePrefix+"/"+img] = fn
			}
		}
	}

	if _, exists := functionAliases["apply-replacements"]; exists {
		applyMappings(functionAliases["apply-replacements"], replacements.ApplyReplacements)
	}
	if _, exists := functionAliases["set-namespace"]; exists {
		applyMappings(functionAliases["set-namespace"], setNamespace.Run)
	}
	if _, exists := functionAliases["starlark"]; exists {
		applyMappings(functionAliases["starlark"], starlark.Process)
	}
}

func (s *FunctionConfigStore) DeleteFunctionConfig(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.functionConfigurations, key.Name)
}

func (s *FunctionConfigStore) GetFunctionConfig(name string) (*configapi.FunctionConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	config, ok := s.functionConfigurations[name]
	return config, ok
}

func (s *FunctionConfigStore) GetBinaryFromCache(image string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	image, tag := splitImage(image)
	prefixToCheck := util.GetImageRepository(image)
	binaryStore, exists := s.binaryExecutorCache[util.GetImageName(image)]
	if exists {
		regex := regexp.MustCompile(binaryStore.PrefixRegex)
		if regex.MatchString(prefixToCheck) {
			binaryPath, tagExists := binaryStore.Tags[tag]
			if tagExists {
				return binaryPath, true
			}
		}
	}
	return "", false
}

func (s *FunctionConfigStore) GetExecCache() map[string]fnsdk.ResourceListProcessor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.builtInExecutorCache
}

// GetProcessorFromCache looks up a function processor by image, holding the read lock for the duration of the lookup.
func (s *FunctionConfigStore) GetProcessorFromCache(image string) (fnsdk.ResourceListProcessor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	processor, found := s.builtInExecutorCache[image]
	return processor, found
}

func (s *FunctionConfigStore) List() []*configapi.FunctionConfig {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Collect(maps.Values(s.functionConfigurations))
}

type ReconcilerFor string

const (
	ReconcilerForFunctionRunner ReconcilerFor = "function-runner"
	ReconcilerForServer         ReconcilerFor = "server"
	ReconcilerForController     ReconcilerFor = "controller"
)

type FunctionConfigReconciler struct {
	Client              client.Client
	FunctionConfigStore *FunctionConfigStore
	// For indicates which component the reconciler is collecting the configs for
	// TODO: remove after merging of function-runner into server
	For ReconcilerFor
}

func (r *FunctionConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, finalErr error) {
	klog.Infof("FunctionConfig %q changed", req.NamespacedName)
	obj := &configapi.FunctionConfig{}
	err := r.Client.Get(ctx, req.NamespacedName, obj)
	if apierrors.IsNotFound(err) {
		r.FunctionConfigStore.DeleteFunctionConfig(req.NamespacedName)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if obj.DeletionTimestamp != nil {
		if err := r.removeFinalizer(ctx, obj); err != nil {
			return ctrl.Result{}, err
		}

		r.FunctionConfigStore.DeleteFunctionConfig(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	if err := r.addFinalizer(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		patch := client.MergeFrom(obj.DeepCopy())

		if finalErr != nil {
			obj.Status.Error = finalErr.Error()
		} else {
			obj.Status.Error = ""
			switch r.For {
			case ReconcilerForFunctionRunner:
				obj.Status.FunctionRunnerObservedGeneration = obj.Generation
			case ReconcilerForServer:
				obj.Status.ApiServerObservedGeneration = obj.Generation
			case ReconcilerForController:
				obj.Status.ControllerObservedGeneration = obj.Generation
			}
		}

		if err := r.Client.Status().Patch(ctx, obj, patch); err != nil {
			klog.Errorf("Failed to update status of FunctionConfig %q: %v", obj.Name, err)
		}
	}()

	// Check if the FunctionConfig already exists in the store with a different name to avoid duplications
	image := obj.Spec.Image
	fc, exists := r.FunctionConfigStore.GetFunctionConfig(image)

	if exists && fc.Name != obj.Name {
		klog.Infof("FunctionConfig for %s image is already in the store with a different name", image)
		return ctrl.Result{}, nil
	}

	r.FunctionConfigStore.UpsertFunctionConfig(obj.Name, obj)

	if obj.Spec.BinaryExecutor != nil {
		r.FunctionConfigStore.UpdateBinaryCache(obj.Name, obj)
	}

	if obj.Spec.GoExecutor != nil {
		r.FunctionConfigStore.UpdateExecCache(obj.Name, obj)
	}

	return ctrl.Result{}, nil
}

func (r *FunctionConfigReconciler) removeFinalizer(ctx context.Context, obj *configapi.FunctionConfig) error {
	patch := client.MergeFrom(obj.DeepCopy())

	switch r.For {
	case ReconcilerForFunctionRunner:
		controllerutil.RemoveFinalizer(obj, FunctionRunnerFinalizer)
	case ReconcilerForServer:
		controllerutil.RemoveFinalizer(obj, ServerFinalizer)
	case ReconcilerForController:
		controllerutil.RemoveFinalizer(obj, ControllerFinalizer)
	}

	if err := r.Client.Patch(ctx, obj, patch); err != nil {
		klog.Errorf("Failed to remove finalizer from FunctionConfig %q: %v", obj.Name, err)
		return err
	}

	return nil
}

func (r *FunctionConfigReconciler) addFinalizer(ctx context.Context, obj *configapi.FunctionConfig) error {
	patch := client.MergeFrom(obj.DeepCopy())

	updated := false
	switch r.For {
	case ReconcilerForFunctionRunner:
		updated = controllerutil.AddFinalizer(obj, FunctionRunnerFinalizer)
	case ReconcilerForServer:
		updated = controllerutil.AddFinalizer(obj, ServerFinalizer)
	case ReconcilerForController:
		updated = controllerutil.AddFinalizer(obj, ControllerFinalizer)
	}

	if updated {
		if err := r.Client.Patch(ctx, obj, patch); err != nil {
			klog.Errorf("Failed to add finalizer to FunctionConfig %q: %v", obj.Name, err)
			return err
		}
	}

	return nil
}
