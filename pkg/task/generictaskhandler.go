// Copyright 2022, 2025-2026 The kpt Authors
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

package task

import (
	"context"
	"fmt"
	"maps"
	"path"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/builtins/builtintypes"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	kptfn "github.com/kptdev/krm-functions-sdk/go/fn"
	kptfileko "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	porchapi "github.com/kptdev/porch/api/porch"
	porchapiv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/kyaml/comments"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

var _ TaskHandler = &genericTaskHandler{}

type genericTaskHandler struct {
	runnerOptionsResolver      func(namespace string) runneroptions.RunnerOptions
	runtime                    fn.FunctionRuntime
	repoOpener                 repository.RepositoryOpener
	credentialResolver         repository.CredentialResolver
	referenceResolver          repository.ReferenceResolver
	repoOperationRetryAttempts int
}

func (th *genericTaskHandler) GetRuntime() fn.FunctionRuntime {
	return th.runtime
}

func (th *genericTaskHandler) SetRunnerOptionsResolver(runnerOptionsResolver func(namespace string) runneroptions.RunnerOptions) {
	th.runnerOptionsResolver = runnerOptionsResolver
}

func (th *genericTaskHandler) SetRuntime(runtime fn.FunctionRuntime) {
	th.runtime = runtime
}

func (th *genericTaskHandler) SetRepoOpener(repoOpener repository.RepositoryOpener) {
	th.repoOpener = repoOpener
}

func (th *genericTaskHandler) SetCredentialResolver(credentialResolver repository.CredentialResolver) {
	th.credentialResolver = credentialResolver
}

func (th *genericTaskHandler) SetReferenceResolver(referenceResolver repository.ReferenceResolver) {
	th.referenceResolver = referenceResolver
}

func (th *genericTaskHandler) SetRepoOperationRetryAttempts(retryAttempts int) {
	th.repoOperationRetryAttempts = retryAttempts
}

func (th *genericTaskHandler) ApplyTask(ctx context.Context, draft repository.PackageRevisionDraft, repositoryObj *configapi.Repository, obj *porchapiv1alpha1.PackageRevision, packageConfig *builtintypes.PackageConfig) error {
	if len(obj.Spec.Tasks) != 1 {
		return pkgerrors.New("task list must contain exactly 1 task")
	}

	mut, err := th.mapTaskToMutation(obj, &obj.Spec.Tasks[0], repositoryObj.Spec.Deployment, packageConfig)
	if err != nil {
		return err
	}

	resources, taskResult, err := mut.apply(ctx, repository.PackageResources{})
	if err != nil {
		return err
	}

	// Upsert labels/annotations/readinessGates from obj.Spec.PackageMetadata and obj.Spec.ReadinessGates
	kptFile, err := kptfileko.NewFromPackage(resources.Contents)
	if err != nil {
		return pkgerrors.Wrap(err, "failed to parse Kptfile")
	}

	if _, err := applyMetadataToKptfile(kptFile, obj, false); err != nil {
		return pkgerrors.Wrap(err, "failed to apply metadata to Kptfile")
	}

	if err := kptFile.WriteToPackage(resources.Contents); err != nil {
		return pkgerrors.Wrap(err, "failed to write to Kptfile")
	}

	// Render package after creation.
	draftMeta := draft.GetMeta()
	resources, _, err = th.renderMutation(draftMeta.GetNamespace()).apply(ctx, resources)
	if err != nil {
		return err
	}

	prr := &porchapiv1alpha1.PackageRevisionResources{
		Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
			Resources: resources.Contents,
		},
	}

	return draft.UpdateResources(ctx, prr, taskResult.Task)
}

func (th *genericTaskHandler) DoPRMutations(
	ctx context.Context,
	repoPR repository.PackageRevision,
	oldObj, newObj *porchapiv1alpha1.PackageRevision,
	draft repository.PackageRevisionDraft) error {
	ctx, span := tracer.Start(ctx, "genericTaskHandler::DoPRMutations", trace.WithAttributes())
	defer span.End()

	// Update package contents only if the package is in draft state
	if oldObj.Spec.Lifecycle != porchapiv1alpha1.PackageRevisionLifecycleDraft {
		return nil
	}

	subpackageDir, err := porchapiv1alpha1.GetSubpackageDir(newObj)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to apply subpackage task to %s, subpackageDir is invalid", draft.Key())
	}

	apiResources, err := repoPR.GetResources(ctx)
	if err != nil {
		return fmt.Errorf("cannot get package resources: %w", err)
	}
	resources := repository.PackageResources{
		Contents: apiResources.Spec.Resources,
	}

	if subpackageDir != "" {
		if err := th.applySubpackageTask(ctx, draft, newObj, resources); err != nil {
			return pkgerrors.Wrapf(err, "failed to apply subpackage task to %s", draft.Key())
		}
	}

	newKptfileContent, changed, err := PatchKptfile(ctx, repoPR, newObj)
	if err != nil {
		return err
	}
	if changed && newKptfileContent != "" && newKptfileContent != "{}\n" {
		resources.Contents[kptfilev1.KptFileName] = newKptfileContent
	}

	// render
	draftMeta := draft.GetMeta()
	resources, _, err = th.renderMutation(draftMeta.GetNamespace()).apply(ctx, resources)
	if err != nil {
		klog.Error(err)
		return renderError(err)
	}

	prr := &porchapiv1alpha1.PackageRevisionResources{
		Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
			Resources: resources.Contents,
		},
	}

	return draft.UpdateResources(ctx, prr, &porchapiv1alpha1.Task{Type: porchapiv1alpha1.TaskTypeRender})
}

func (th *genericTaskHandler) DoPRResourceMutations(
	ctx context.Context,
	pr2Update repository.PackageRevision,
	draft repository.PackageRevisionDraft,
	oldRes, newRes *porchapiv1alpha1.PackageRevisionResources) (*porchapiv1alpha1.RenderStatus, error) {
	ctx, span := tracer.Start(ctx, "genericTaskHandler::DoPRResourceMutations", trace.WithAttributes())
	defer span.End()

	mut := &replaceResourcesMutation{
		newResources: newRes,
		oldResources: oldRes,
	}
	prevResources, err := pr2Update.GetResources(ctx)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "cannot get package resources")
	}
	resources := repository.PackageResources{
		Contents: prevResources.Spec.Resources,
	}

	appliedResources, _, err := mut.apply(ctx, resources)
	if err != nil {
		return nil, err
	}

	// Render the package
	// Render failure will fail the overall API operation.
	// The render error and result are captured as part of renderStatus above
	// and are returned in the PackageRevisionResources API's status field.
	// The renderMutation always returns resources (kpt controls unrendered vs
	// partially-rendered via the Kptfile annotation kpt.dev/save-on-render-failure).
	var (
		renderStatus *porchapiv1alpha1.RenderStatus
		renderResult *porchapiv1alpha1.TaskResult
	)
	appliedResources, renderResult, rendErr := th.renderMutation(oldRes.GetNamespace()).apply(ctx, appliedResources)
	// keep last render result on empty patch
	if renderResult != nil &&
		renderResult.RenderStatus != nil &&
		(renderResult.RenderStatus.Err != "" ||
			len(renderResult.RenderStatus.Result.Items) != 0) {
		renderStatus = renderResult.RenderStatus
	}
	prr := &porchapiv1alpha1.PackageRevisionResources{
		Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
			Resources: appliedResources.Contents,
		},
	}
	if rendErr != nil {
		klog.Error(rendErr)
		err := draft.UpdateResources(ctx, prr, &porchapiv1alpha1.Task{Type: porchapiv1alpha1.TaskTypeRender})
		if err != nil {
			return renderStatus, &RenderPersistError{RenderErr: rendErr, PersistErr: err}
		}
		return renderStatus, &RenderError{Err: rendErr}
	}

	return renderStatus, draft.UpdateResources(ctx, prr, &porchapiv1alpha1.Task{Type: porchapiv1alpha1.TaskTypeRender})
}

func (th *genericTaskHandler) applySubpackageTask(
	ctx context.Context,
	draft repository.PackageRevisionDraft,
	obj *porchapiv1alpha1.PackageRevision,
	resources repository.PackageResources) error {
	ctx, span := tracer.Start(ctx, "genericTaskHandler::applySubpackageTask", trace.WithAttributes())
	defer span.End()

	if len(obj.Spec.Tasks) != 2 {
		return pkgerrors.New("for subpackage tasks, the task list must contain exactly 2 tasks, the source task followed by the subpackage task")
	}

	var repo configapi.Repository
	if err := th.referenceResolver.ResolveReference(ctx, draft.Key().RKey().K8SNS(), draft.Key().RKey().K8SName(), &repo); err != nil {
		return pkgerrors.Wrapf(err, "cannot find repository for draft PR %+v", draft.Key())
	}

	mut, err := th.mapTaskToMutation(obj, &obj.Spec.Tasks[1], repo.Spec.Deployment, nil)
	if err != nil {
		return err
	}

	subpackageResources, taskResult, err := mut.apply(ctx, repository.PackageResources{})
	if err != nil {
		return err
	}

	kptFile, err := kptfileko.NewFromPackage(subpackageResources.Contents)
	if err != nil {
		return pkgerrors.Wrap(err, "failed to parse subpackage Kptfile")
	}

	subpackageDir, err := porchapiv1alpha1.GetSubpackageDir(obj)
	if err != nil {
		return err
	}

	subpackageName, _ := porchapi.ComposeSubpkgObjName(subpackageDir)
	if err := kptFile.SetName(subpackageName); err != nil {
		return pkgerrors.Wrapf(err, "failed to write package name %q to subpackage Kptfile", subpackageName)
	}

	if err := kptFile.WriteToPackage(subpackageResources.Contents); err != nil {
		return pkgerrors.Wrapf(err, "failed to write to subpackage Kptfile %q", path.Join(subpackageDir, kptfilev1.KptFileName))
	}

	// Remove the subpackage task to prevent re-execution of the task
	obj.Spec.Tasks = obj.Spec.Tasks[:1]

	switch taskResult.Task.Type {
	case porchapiv1alpha1.TaskTypeClone:
		return th.insertSubpackageResourcesInDraftResources(ctx, subpackageDir, resources, subpackageResources)
	case porchapiv1alpha1.TaskTypeUpgrade:
		return th.upgradeSubpackageResourcesInDraftResources(ctx, subpackageDir, resources, subpackageResources)
	default:
		return fmt.Errorf("task of type %q not supported for subpackages", taskResult.Task.Type)
	}
}

func (th *genericTaskHandler) renderMutation(namespace string) mutation {
	return &renderPackageMutation{
		runnerOptions: th.runnerOptionsResolver(namespace),
		runtime:       th.runtime,
	}
}

func renderError(err error) error {
	return pkgerrors.Wrap(err, "Error rendering package in kpt function pipeline. Package NOT pushed to remote. Fix locally (until 'kpt fn render' succeeds) and retry. Details")
}

func (th *genericTaskHandler) mapTaskToMutation(obj *porchapiv1alpha1.PackageRevision, task *porchapiv1alpha1.Task, isDeployment bool, packageConfig *builtintypes.PackageConfig) (mutation, error) {
	switch task.Type {
	case porchapiv1alpha1.TaskTypeInit:
		if task.Init == nil {
			return nil, fmt.Errorf("init not set for task of type %q", task.Type)
		}
		return &initPackageMutation{
			name: obj.Spec.PackageName,
			task: task,
		}, nil
	case porchapiv1alpha1.TaskTypeClone:
		if task.Clone == nil {
			return nil, fmt.Errorf("clone not set for task of type %q", task.Type)
		}
		return &clonePackageMutation{
			task:                       task,
			namespace:                  obj.Namespace,
			name:                       obj.Spec.PackageName,
			isDeployment:               isDeployment,
			repoOpener:                 th.repoOpener,
			credentialResolver:         th.credentialResolver,
			referenceResolver:          th.referenceResolver,
			repoOperationRetryAttempts: th.repoOperationRetryAttempts,
			packageConfig:              packageConfig,
		}, nil

	case porchapiv1alpha1.TaskTypeUpgrade:
		if task.Upgrade == nil {
			return nil, fmt.Errorf("upgrade field not set for task of type %q", task.Type)
		}
		return &upgradePackageMutation{
			upgradeTask:       task,
			namespace:         obj.Namespace,
			repoOpener:        th.repoOpener,
			referenceResolver: th.referenceResolver,
			pkgName:           obj.Spec.PackageName,
		}, nil

	case porchapiv1alpha1.TaskTypeEdit:
		if task.Edit == nil {
			return nil, fmt.Errorf("edit not set for task of type %q", task.Type)
		}
		return &editPackageMutation{
			task:              task,
			namespace:         obj.Namespace,
			packageName:       obj.Spec.PackageName,
			repositoryName:    obj.Spec.RepositoryName,
			repoOpener:        th.repoOpener,
			referenceResolver: th.referenceResolver,
		}, nil

	default:
		return nil, fmt.Errorf("task of type %q not supported", task.Type)
	}
}

// insertSubpackageResourcesInDraftResources adds the resources of the independent subpackage to the parent package revision
// at `SubpackageDir`
func (th *genericTaskHandler) insertSubpackageResourcesInDraftResources(ctx context.Context, subpackageDir string, parentResources, subpackageResources repository.PackageResources) error {
	logger := log.FromContext(ctx)
	logger.V(1).Info("cloning subpackage resources into parent at ", "subpackageDir", subpackageDir)

	for resourceKey := range parentResources.Contents {
		if parentSubpackageDir := th.parentSubpackageFound(subpackageDir, resourceKey); parentSubpackageDir != "" {
			return fmt.Errorf("cannot clone subpackage into another subpackage, parent already has a subpackage at %q (requested subpackageDir: %q)", parentSubpackageDir, subpackageDir)
		}

		if strings.HasPrefix(resourceKey, subpackageDir+"/") {
			return fmt.Errorf("cannot clone subpackage into parent, parent already has content at %q", subpackageDir)
		}
	}

	for subpackageResourceKey, subpackageResourceValue := range subpackageResources.Contents {
		parentResources.Contents[subpackageDir+"/"+subpackageResourceKey] = subpackageResourceValue
	}

	logger.V(1).Info("cloned subpackage resources into parent at ", "subpackageDir", subpackageDir)
	return nil
}

// upgradeSubpackageResourcesInDraftResources updates the resources of the independent subpackage in the parent package revision
// at `SubpackageDir`
func (th *genericTaskHandler) upgradeSubpackageResourcesInDraftResources(ctx context.Context, subpackageDir string, parentResources, subpackageResources repository.PackageResources) error {
	logger := log.FromContext(ctx)
	logger.V(1).Info("upgrading subpackage resources in parent at ", "subpackageDir", subpackageDir)

	subpackageFound := false
	for resourceKey := range parentResources.Contents {
		if resourceKey == subpackageDir {
			return fmt.Errorf("cannot upgrade subpackage in parent, parent already has content at %q", subpackageDir)
		}

		if strings.HasPrefix(resourceKey, subpackageDir+"/") {
			subpackageFound = true
			delete(parentResources.Contents, resourceKey)
			continue
		}

		if parentSubpackageDir := th.parentSubpackageFound(subpackageDir, resourceKey); parentSubpackageDir != "" {
			return fmt.Errorf("cannot upgrade subpackage in another subpackage, parent already has a subpackage at %q (requested subpackageDir: %q)", parentSubpackageDir, subpackageDir)
		}

	}

	if !subpackageFound {
		return fmt.Errorf("cannot find subpackage in parent, parent does not have a subpackage at %q", subpackageDir)
	}

	for subpackageResourceKey, subpackageResourceValue := range subpackageResources.Contents {
		parentResources.Contents[subpackageDir+"/"+subpackageResourceKey] = subpackageResourceValue
	}

	logger.V(1).Info("upgraded subpackage resources in parent at ", "subpackageDir", subpackageDir)
	return nil
}

func (th *genericTaskHandler) parentSubpackageFound(subpackageDir, resourceKey string) string {
	if strings.HasSuffix(resourceKey, kptfilev1.KptFileName) {
		resourceKey = strings.TrimSuffix(resourceKey, "/"+kptfilev1.KptFileName)
	} else {
		return ""
	}

	if subpackageDir == resourceKey || strings.HasPrefix(subpackageDir, resourceKey+"/") {
		return resourceKey
	}

	return ""
}

func PatchKptfile(
	ctx context.Context,
	oldPackage repository.PackageRevision,
	newObj *porchapiv1alpha1.PackageRevision,
) (string, bool, error) {
	res, err := oldPackage.GetResources(ctx)
	if err != nil {
		return "", false, fmt.Errorf("getting resources: %w", err)
	}
	resourceMap := map[string]string{}
	if res != nil {
		resourceMap = res.Spec.Resources
	}

	kptFile, err := kptfileko.NewFromPackage(resourceMap)
	if err != nil {
		return "", false, fmt.Errorf("parse Kptfile: %w", err)
	}

	changed, err := applyMetadataToKptfile(kptFile, newObj, true)
	if err != nil {
		return "", false, err
	}

	if newObj.Status.Conditions != nil {
		desiredMap := make(map[string]kptfilev1.Condition)
		for _, c := range newObj.Status.Conditions {
			desiredMap[c.Type] = kptfilev1.Condition{
				Type:    c.Type,
				Status:  convertStatusToKptfile(c.Status),
				Reason:  c.Reason,
				Message: c.Message,
			}
		}

		existingSub := kptFile.Conditions()
		finalConditions := make(kptfn.SliceSubObjects, 0, len(desiredMap))
		hasChanged := false

		for _, so := range existingSub {
			condType := so.GetString("type")
			if desiredCond, found := desiredMap[condType]; found {
				isDifferent := so.GetString("status") != string(desiredCond.Status) ||
					so.GetString("reason") != desiredCond.Reason ||
					so.GetString("message") != desiredCond.Message

				if isDifferent {
					hasChanged = true
					_ = so.SetNestedString(string(desiredCond.Status), "status")
					if desiredCond.Reason != "" {
						_ = so.SetNestedString(desiredCond.Reason, "reason")
					} else {
						_, _ = so.RemoveNestedField("reason")
					}
					if desiredCond.Message != "" {
						_ = so.SetNestedString(desiredCond.Message, "message")
					} else {
						_, _ = so.RemoveNestedField("message")
					}
				}
				finalConditions = append(finalConditions, so)
				delete(desiredMap, condType)
			} else {
				hasChanged = true
			}
		}

		if len(desiredMap) > 0 {
			hasChanged = true
			for _, newCond := range desiredMap {
				ko, err := kptfn.NewFromTypedObject(newCond)
				if err != nil {
					return "", false, fmt.Errorf("convert new condition: %w", err)
				}
				finalConditions = append(finalConditions, &ko.SubObject)
			}
		}

		if hasChanged {
			changed = true
			if err := kptFile.SetConditions(finalConditions); err != nil {
				return "", false, fmt.Errorf("set final conditions: %w", err)
			}
		}
	}

	if !changed {
		return "", false, nil
	}

	if err := kptFile.WriteToPackage(resourceMap); err != nil {
		return "", false, fmt.Errorf("write Kptfile: %w", err)
	}
	content := resourceMap[kptfilev1.KptFileName]
	return content, true, nil
}

func applyMetadataToKptfile(kptFile *kptfileko.KptfileKubeObject, obj *porchapiv1alpha1.PackageRevision, replace bool) (bool, error) {
	var changed bool

	if obj.Spec.PackageMetadata != nil {
		if obj.Spec.PackageMetadata.Labels != nil {
			if applyMapMetadata(kptFile.GetLabels(), obj.Spec.PackageMetadata.Labels, replace, kptFile.SetLabels) {
				changed = true
			}
		}
		if obj.Spec.PackageMetadata.Annotations != nil {
			if applyMapMetadata(kptFile.GetAnnotations(), obj.Spec.PackageMetadata.Annotations, replace, kptFile.SetAnnotations) {
				changed = true
			}
		}
	}

	if obj.Spec.ReadinessGates != nil {
		if gatesChanged, err := syncReadinessGates(kptFile, obj.Spec.ReadinessGates); err != nil {
			return false, err
		} else if gatesChanged {
			changed = true
		}
	}

	return changed, nil
}

func applyMapMetadata(cur, desired map[string]string, replace bool, setter func(map[string]string)) bool {
	if replace {
		if !maps.Equal(cur, desired) {
			setter(desired)
			return true
		}
		return false
	}
	changed := false
	for k, v := range desired {
		if cv, ok := cur[k]; !ok || cv != v {
			cur[k] = v
			changed = true
		}
	}
	if changed {
		setter(cur)
	}
	return changed
}

func syncReadinessGates(kptFile *kptfileko.KptfileKubeObject, desired []porchapiv1alpha1.ReadinessGate) (bool, error) {
	desiredMap := make(map[string]porchapiv1alpha1.ReadinessGate, len(desired))
	for _, rg := range desired {
		desiredMap[rg.ConditionType] = rg
	}
	final := make(kptfn.SliceSubObjects, 0, len(desiredMap))
	changed := false
	for _, so := range kptFile.ReadinessGates() {
		if _, found := desiredMap[so.GetString("conditionType")]; found {
			final = append(final, so)
			delete(desiredMap, so.GetString("conditionType"))
		} else {
			changed = true
		}
	}
	for _, newGate := range desiredMap {
		changed = true
		ko, err := kptfn.NewFromTypedObject(newGate)
		if err != nil {
			return false, fmt.Errorf("convert new readiness gate: %w", err)
		}
		final = append(final, &ko.SubObject)
	}
	if changed {
		if err := kptFile.SetReadinessGates(final); err != nil {
			return false, fmt.Errorf("set final readiness gates: %w", err)
		}
	}
	return changed, nil
}

func convertStatusToKptfile(s porchapiv1alpha1.ConditionStatus) kptfilev1.ConditionStatus {
	switch s {
	case porchapiv1alpha1.ConditionTrue:
		return kptfilev1.ConditionTrue
	case porchapiv1alpha1.ConditionFalse:
		return kptfilev1.ConditionFalse
	case porchapiv1alpha1.ConditionUnknown:
		return kptfilev1.ConditionUnknown
	default:
		panic(fmt.Errorf("unknown condition status: %v", s))
	}
}

func healConfig(old, new map[string]string) (map[string]string, error) {
	// Copy comments from old config to new
	oldResources, err := (&packageReader{
		input: repository.PackageResources{Contents: old},
		extra: map[string]string{},
	}).Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read old package resources: %w", err)
	}

	var filter kio.FilterFunc = func(r []*yaml.RNode) ([]*yaml.RNode, error) {
		for _, n := range r {
			for _, original := range oldResources {
				if n.GetNamespace() == original.GetNamespace() &&
					n.GetName() == original.GetName() &&
					n.GetApiVersion() == original.GetApiVersion() &&
					n.GetKind() == original.GetKind() &&
					// Support ytt templates with #@ and #! comments
					!strings.Contains(n.MustString(), "#@") && !strings.Contains(original.MustString(), "#@") &&
					!strings.Contains(n.MustString(), "#!") && !strings.Contains(original.MustString(), "#!") {

					err = comments.CopyComments(original, n)
					if err != nil {
						return nil, fmt.Errorf("failed to copy comments: %w", err)
					}
				}
			}
		}
		return r, nil
	}

	out := &packageWriter{
		output: repository.PackageResources{
			Contents: map[string]string{},
		},
	}

	extra := map[string]string{}

	if err := (kio.Pipeline{
		Inputs: []kio.Reader{&packageReader{
			input: repository.PackageResources{Contents: new},
			extra: extra,
		}},
		Filters:               []kio.Filter{filter},
		Outputs:               []kio.Writer{out},
		ContinueOnEmptyResult: true,
	}).Execute(); err != nil {
		return nil, err
	}

	healed := out.output.Contents

	for k, v := range extra {
		healed[k] = v
	}

	return healed, nil
}
