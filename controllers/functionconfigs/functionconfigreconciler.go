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

package functionconfigs

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/kptdev/krm-functions-catalog/functions/go/apply-replacements/replacements"
	setNamespace "github.com/kptdev/krm-functions-catalog/functions/go/set-namespace/transformer"
	"github.com/kptdev/krm-functions-catalog/functions/go/starlark/starlark"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	imageutil "github.com/kptdev/porch/pkg/util/image"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const BaseFinalizer = "config.porch.kpt.dev/functionconfig"
const ServerFinalizer = BaseFinalizer + "-porch-server"
const FunctionRunnerFinalizer = BaseFinalizer + "-function-runner"
const ControllerFinalizer = BaseFinalizer + "-controller"

func validateSemverConstraints(tags []string, allowedWildcards ...string) error {
	for _, tag := range tags {
		if slices.Contains(allowedWildcards, tag) {
			continue
		}
		if _, err := semver.NewConstraint(tag); err != nil {
			return fmt.Errorf("tag %q is not a valid semver constraint: %w", tag, err)
		}
	}
	return nil
}

func deduplicateStringSlice(s []string) []string {
	if len(s) <= 1 {
		return s
	}
	seen := make(map[string]struct{}, len(s))
	ordered := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			ordered = append(ordered, v)
		}
	}
	return ordered
}

func normalizeSpec(spec *configapi.FunctionConfigSpec) bool {
	changed := false

	toDedupe := []*[]string{&spec.Prefixes}
	if spec.PodExecutor != nil {
		toDedupe = append(toDedupe, &spec.PodExecutor.Tags)
	}
	if spec.BinaryExecutor != nil {
		toDedupe = append(toDedupe, &spec.BinaryExecutor.Tags)
	}
	if spec.GoExecutor != nil {
		toDedupe = append(toDedupe, &spec.GoExecutor.Tags)
	}

	for _, slice := range toDedupe {
		prevLen := len(*slice)
		*slice = deduplicateStringSlice(*slice)
		changed = changed || prevLen != len(*slice)
	}

	return changed
}

type BinaryCacheEntry struct {
	PrefixRegex *regexp.Regexp
	Tags        []string
	AbsPath     string
}

type BuiltInCacheEntry struct {
	PrefixRegex *regexp.Regexp
	Process     fnsdk.ResourceListProcessor
	Tags        []string
}

type FunctionConfigStore struct {
	mu sync.RWMutex

	functionConfigurations map[string]*configapi.FunctionConfig
	binaryExecutorCache    map[string]BinaryCacheEntry
	builtInExecutorCache   map[string]BuiltInCacheEntry

	defaultImagePrefix string
	defaultBinaryDir   string
}

func NewFunctionConfigStore(defaultImagePrefix, defaultBinaryDir string) *FunctionConfigStore {
	return &FunctionConfigStore{
		functionConfigurations: make(map[string]*configapi.FunctionConfig),
		binaryExecutorCache:    make(map[string]BinaryCacheEntry),
		builtInExecutorCache:   make(map[string]BuiltInCacheEntry),
		defaultImagePrefix:     strings.TrimRight(defaultImagePrefix, "/"),
		defaultBinaryDir:       strings.TrimRight(defaultBinaryDir, "/"),
	}
}

func (s *FunctionConfigStore) UpsertFunctionConfig(name string, obj *configapi.FunctionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.functionConfigurations[name] = obj
}

func (s *FunctionConfigStore) generateRegexPattern(prefixes []string) *regexp.Regexp {
	var preparedPrefixes []string
	for _, prefix := range prefixes {
		if prefix == "" {
			preparedPrefixes = append(preparedPrefixes, regexp.QuoteMeta(s.defaultImagePrefix))
		} else {
			preparedPrefixes = append(preparedPrefixes, regexp.QuoteMeta(prefix))
		}
	}

	return regexp.MustCompile("^(?:" + strings.Join(preparedPrefixes, "|") + ")$")

}

func (s *FunctionConfigStore) UpdateBinaryCache(spec *configapi.FunctionConfigSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()

	abs := spec.BinaryExecutor.Path
	if abs[0] != '/' {
		var err error
		abs, err = filepath.Abs(filepath.Join(s.defaultBinaryDir, spec.BinaryExecutor.Path))
		if err != nil {
			klog.Warningf("Failed to cache %q: %v", spec.Image, err)
			return
		}
	}

	s.binaryExecutorCache[spec.Image] = BinaryCacheEntry{
		Tags:        spec.BinaryExecutor.Tags,
		PrefixRegex: s.generateRegexPattern(spec.Prefixes),
		AbsPath:     abs,
	}
}

func (s *FunctionConfigStore) UpdateExecCache(name string, functionConfig *configapi.FunctionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := name
	if functionConfig.Spec.GoExecutor.ID != nil {
		id = *functionConfig.Spec.GoExecutor.ID
	}

	applyMappings := func(id string, fn fnsdk.ResourceListProcessorFunc) {
		//Clear previous entries for the actual function
		for img := range s.builtInExecutorCache {
			if strings.Contains(img, name) {
				delete(s.builtInExecutorCache, img)
			}
		}

		s.builtInExecutorCache[id] = BuiltInCacheEntry{
			Process:     fn,
			Tags:        functionConfig.Spec.GoExecutor.Tags,
			PrefixRegex: s.generateRegexPattern(functionConfig.Spec.Prefixes),
		}
	}

	switch {
	case functionConfig.Name == "apply-replacements":
		applyMappings(id, replacements.ApplyReplacements)
	case functionConfig.Name == "set-namespace":
		applyMappings(id, setNamespace.Run)
	case functionConfig.Name == "starlark":
		applyMappings(id, starlark.Process)
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
	return s.GetBinaryFromCacheByConstraint(image, imageutil.Parse(image).Tag)
}

func (s *FunctionConfigStore) GetBinaryFromCacheByConstraint(image, tag string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	parsedImage := imageutil.Parse(image)
	cacheEntry, ok := s.binaryExecutorCache[parsedImage.BaseName]
	if !ok || !cacheEntry.PrefixRegex.MatchString(parsedImage.Prefix()) || !imageutil.MatchesConfigTags(tag, cacheEntry.Tags) {
		return "", false
	}
	return cacheEntry.AbsPath, true
}

func (s *FunctionConfigStore) GetExecCache() map[string]BuiltInCacheEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.builtInExecutorCache
}

// GetProcessorFromCache looks up a function processor by image, holding the read lock for the duration of the lookup.
func (s *FunctionConfigStore) GetProcessorFromCache(image string) (fnsdk.ResourceListProcessor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	parsedImage := imageutil.Parse(image)
	entry, found := s.builtInExecutorCache[parsedImage.BaseName]
	prefixToCheck := parsedImage.Prefix()
	if prefixToCheck == "" {
		prefixToCheck = s.defaultImagePrefix
	}
	if !found || !imageutil.MatchesAnyConstraint(parsedImage.Tag, entry.Tags) || !entry.PrefixRegex.MatchString(prefixToCheck) {
		return nil, false
	}
	return entry.Process, true
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

type Reconciler struct {
	Client              client.Client
	FunctionConfigStore *FunctionConfigStore
	// For indicates which component the reconciler is collecting the configs for
	// TODO: remove after merging of function-runner into server
	For ReconcilerFor
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configapi.FunctionConfig{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, finalErr error) {
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
			if finalErr == nil {
				finalErr = err
			}
		}
	}()

	// Check if the FunctionConfig already exists in the store with a different name to avoid duplications
	image := obj.Spec.Image
	fc, exists := r.FunctionConfigStore.GetFunctionConfig(image)

	if exists && fc.Name != obj.Name {
		klog.Infof("FunctionConfig for %s image is already in the store with a different name", image)
		return ctrl.Result{}, nil
	}

	if obj.Spec.PodExecutor != nil {
		if err := validateSemverConstraints(obj.Spec.PodExecutor.Tags, "*", "", "latest"); err != nil {
			return ctrl.Result{}, fmt.Errorf("invalid PodExecutor tag constraints: %w", err)
		}
	}
	if obj.Spec.BinaryExecutor != nil {
		if err := validateSemverConstraints(obj.Spec.BinaryExecutor.Tags, "latest"); err != nil {
			return ctrl.Result{}, fmt.Errorf("invalid BinaryExecutor tag constraints: %w", err)
		}
	}
	if obj.Spec.GoExecutor != nil {
		if err := validateSemverConstraints(obj.Spec.GoExecutor.Tags, "latest"); err != nil {
			return ctrl.Result{}, fmt.Errorf("invalid GoExecutor tag constraints: %w", err)
		}
	}

	specPatchBase := client.MergeFrom(obj.DeepCopy())
	if normalizeSpec(&obj.Spec) {
		if err := r.Client.Patch(ctx, obj, specPatchBase); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch normalised spec for FunctionConfig %q: %w", obj.Name, err)
		}
	}

	r.FunctionConfigStore.UpsertFunctionConfig(obj.Name, obj)

	if obj.Spec.BinaryExecutor != nil {
		r.FunctionConfigStore.UpdateBinaryCache(&obj.Spec)
	}

	if obj.Spec.GoExecutor != nil {
		r.FunctionConfigStore.UpdateExecCache(obj.Name, obj)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) removeFinalizer(ctx context.Context, obj *configapi.FunctionConfig) error {
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

func (r *Reconciler) addFinalizer(ctx context.Context, obj *configapi.FunctionConfig) error {
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
