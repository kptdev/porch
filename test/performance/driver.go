// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"fmt"
	"strings"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LifecycleDriver implements package revision lifecycle operations for a specific Porch API version.
type LifecycleDriver interface {
	Version() PorchAPIVersion
	RepositoryAnnotations() map[string]string
	DoLifecycle(repoName, pkgName string, revisionNum int) (pkgRevName string, err error)
	DeletePackageRevision(repoName, pkgName, pkgRevName string, revisionNum int) error
	ListPackageRevisionsForDeletion() ([]DeletionCandidate, error)
}

func NewLifecycleDriver(version PorchAPIVersion, suite *PerfTestSuite) (LifecycleDriver, error) {
	switch version {
	case PorchAPIV1Alpha1:
		return &v1alpha1Driver{baseDriver: baseDriver{suite: suite}}, nil
	case PorchAPIV1Alpha2:
		return &v1alpha2Driver{baseDriver: baseDriver{suite: suite}}, nil
	default:
		return nil, fmt.Errorf("unsupported porch API version %q", version)
	}
}

type baseDriver struct {
	suite *PerfTestSuite
}

type deletionCandidateExtractor func(client.Object) (DeletionCandidate, bool)

func (b *baseDriver) createPackageRevision(obj client.Object, repoName, pkgName string, revisionNum int) error {
	t := b.suite
	start := time.Now()
	if t.enablePrometheus {
		apiVersion := string(t.testOptions.apiVersion)
		telemetry.PerfTestRecordActiveOperation(pkgRevCreate, apiVersion, 1)
		defer telemetry.PerfTestRecordActiveOperation(pkgRevCreate, apiVersion, -1)
	}

	err := retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.Create(t.ctx, obj)
	})
	duration := time.Since(start)

	t.recordPkgRevMetric(repoName, pkgName, revisionNum, pkgRevCreate, OperationMetrics{
		Operation: fmt.Sprintf("%s:%d", pkgRevCreate, revisionNum),
		Duration:  duration,
		Error:     err,
		Timestamp: start,
	})
	t.recordPerfMetric(pkgRevCreate, repoName, pkgName, duration, err)
	if t.enablePrometheus {
		telemetry.PerfTestRecordPackageRevision(pkgRevCreate, err)
	}

	return err
}

func (b *baseDriver) updatePackageRevisionResources(repoName, pkgName, pkgRevName string, revisionNum int) error {
	t := b.suite
	var resources porchapi.PackageRevisionResources

	start := time.Now()
	err := retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, &resources)
	})
	duration := time.Since(start)
	t.recordPkgRevMetric(repoName, pkgName, revisionNum, pkgRevResourcesGet, OperationMetrics{
		Operation: fmt.Sprintf("%s:%d", pkgRevResourcesGet, revisionNum),
		Duration:  duration,
		Error:     err,
		Timestamp: start,
	})
	t.recordPerfMetric(pkgRevResourcesGet, repoName, pkgName, duration, err)
	if err != nil {
		return err
	}

	pkgResources := t.createPackageResources()
	if resources.Spec.Resources == nil {
		resources.Spec.Resources = make(map[string]string)
	}
	for name, content := range pkgResources {
		resources.Spec.Resources[name] = content
	}

	start = time.Now()
	err = retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.Update(t.ctx, &resources)
	})
	duration = time.Since(start)
	t.recordPkgRevMetric(repoName, pkgName, revisionNum, pkgRevUpdate, OperationMetrics{
		Operation: fmt.Sprintf("%s:%d", pkgRevUpdate, revisionNum),
		Duration:  duration,
		Error:     err,
		Timestamp: start,
	})
	t.recordPerfMetric(pkgRevUpdate, repoName, pkgName, duration, err)

	return err
}

type deletionBehavior struct {
	alwaysProposeDeletion bool
	publishedLifecycle    string
	deletionProposed      string
	afterProposeDeletion  func(repoName, pkgName, pkgRevName string, revisionNum int) error
	waitForDeleted        func(pkgRevName string) error
}

func (b *baseDriver) deletePackageRevision(
	repoName, pkgName, pkgRevName string,
	revisionNum int,
	newObj func() client.Object,
	getLifecycle func(client.Object) string,
	proposeDeletion func(client.Object) error,
	behavior deletionBehavior,
) error {
	t := b.suite
	obj := newObj()

	err := retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj)
	})
	if err != nil {
		return err
	}

	initialLifecycle := getLifecycle(obj)
	shouldPropose := behavior.alwaysProposeDeletion || initialLifecycle == behavior.publishedLifecycle

	start := time.Now()
	if shouldPropose {
		err = retry.RetryOnConflict(retryBackoff, func() error {
			if err := t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj); err != nil {
				return err
			}
			return proposeDeletion(obj)
		})
	}
	duration := time.Since(start)
	t.recordPkgRevMetric(repoName, pkgName, revisionNum, pkgRevProposeDeletion, OperationMetrics{
		Operation: fmt.Sprintf("%s:%d", pkgRevProposeDeletion, revisionNum),
		Duration:  duration,
		Error:     err,
		Timestamp: start,
	})
	t.recordPerfMetric(pkgRevProposeDeletion, repoName, pkgName, duration, err)
	if t.enablePrometheus && shouldPropose {
		telemetry.PerfTestRecordLifecycleTransition(initialLifecycle, behavior.deletionProposed, string(t.testOptions.apiVersion), repoName, pkgName, duration, err)
	}
	if err != nil {
		return err
	}

	if shouldPropose && behavior.afterProposeDeletion != nil {
		if err = behavior.afterProposeDeletion(repoName, pkgName, pkgRevName, revisionNum); err != nil {
			return err
		}
	}

	start = time.Now()
	err = retry.RetryOnConflict(retryBackoff, func() error {
		if err := t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj); err != nil {
			return err
		}
		return t.client.Delete(t.ctx, obj)
	})
	if err == nil && behavior.waitForDeleted != nil {
		if waitErr := behavior.waitForDeleted(pkgRevName); waitErr != nil {
			err = waitErr
		}
	}
	duration = time.Since(start)
	t.recordPkgRevMetric(repoName, pkgName, revisionNum, pkgRevDelete, OperationMetrics{
		Operation: fmt.Sprintf("%s:%d", pkgRevDelete, revisionNum),
		Duration:  duration,
		Error:     err,
		Timestamp: start,
	})
	t.recordPerfMetric(pkgRevDelete, repoName, pkgName, duration, err)
	if t.enablePrometheus && shouldPropose {
		telemetry.PerfTestRecordLifecycleTransition(behavior.deletionProposed, "deleted", string(t.testOptions.apiVersion), repoName, pkgName, duration, err)
	}

	return err
}

func (b *baseDriver) listPackageRevisionsForDeletion(list client.ObjectList, extract deletionCandidateExtractor) ([]DeletionCandidate, error) {
	t := b.suite
	if err := t.client.List(t.ctx, list, client.InNamespace(t.testOptions.namespace)); err != nil {
		return nil, err
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}

	prefix := fmt.Sprintf("%s-test-", t.testOptions.namespace)
	candidates := make([]DeletionCandidate, 0, len(items))
	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok {
			continue
		}
		candidate, ok := extract(obj)
		if !ok || !strings.HasPrefix(candidate.RepoName, prefix) {
			continue
		}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func (b *baseDriver) recordListOperation(repoName, pkgName string, revisionNum int, start time.Time, err error) {
	b.recordPkgRevOperation(repoName, pkgName, revisionNum, pkgRevList, start, err)
}

func (b *baseDriver) recordPkgRevOperation(repoName, pkgName string, revisionNum int, opKey string, start time.Time, err error) {
	duration := time.Since(start)
	b.suite.recordPkgRevMetric(repoName, pkgName, revisionNum, opKey, OperationMetrics{
		Operation: fmt.Sprintf("%s:%d", opKey, revisionNum),
		Duration:  duration,
		Error:     err,
		Timestamp: start,
	})
	b.suite.recordPerfMetric(opKey, repoName, pkgName, duration, err)
}

func (b *baseDriver) getPackageRevision(pkgRevName, repoName, pkgName string, revisionNum int, opKey string, obj client.Object) error {
	t := b.suite
	start := time.Now()
	err := retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj)
	})
	b.recordPkgRevOperation(repoName, pkgName, revisionNum, opKey, start, err)
	return err
}

func (b *baseDriver) proposePackage(
	repoName, pkgName, pkgRevName string,
	revisionNum int,
	obj client.Object,
	getLifecycle func(client.Object) string,
	setProposed func(client.Object) error,
	proposedLifecycle string,
) error {
	t := b.suite
	if err := b.getPackageRevision(pkgRevName, repoName, pkgName, revisionNum, pkgRevGet, obj); err != nil {
		return err
	}

	start := time.Now()
	initialLifecycle := getLifecycle(obj)
	err := retry.RetryOnConflict(retryBackoff, func() error {
		if err := t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj); err != nil {
			return err
		}
		return setProposed(obj)
	})
	b.recordPkgRevOperation(repoName, pkgName, revisionNum, pkgRevPropose, start, err)
	if t.enablePrometheus {
		telemetry.PerfTestRecordLifecycleTransition(initialLifecycle, proposedLifecycle, string(t.testOptions.apiVersion), repoName, pkgName, time.Since(start), err)
	}

	return err
}

func (b *baseDriver) approvePackage(
	repoName, pkgName, pkgRevName string,
	revisionNum int,
	obj client.Object,
	publish func(client.Object) error,
	proposedLifecycle, publishedLifecycle string,
) error {
	t := b.suite
	if err := b.getPackageRevision(pkgRevName, repoName, pkgName, revisionNum, pkgRevGetProposed, obj); err != nil {
		return err
	}

	start := time.Now()
	err := retry.RetryOnConflict(retryBackoff, func() error {
		if err := t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj); err != nil {
			return err
		}
		return publish(obj)
	})
	b.recordPkgRevOperation(repoName, pkgName, revisionNum, pkgRevPublished, start, err)
	if t.enablePrometheus {
		telemetry.PerfTestRecordLifecycleTransition(proposedLifecycle, publishedLifecycle, string(t.testOptions.apiVersion), repoName, pkgName, time.Since(start), err)
	}

	return err
}

func (t *PerfTestSuite) recordPerfMetric(operation, repoName, pkgName string, duration time.Duration, err error) {
	if !t.enablePrometheus {
		return
	}
	telemetry.PerfTestRecordMetric(operation, string(t.testOptions.apiVersion), repoName, pkgName, duration, err)
}

func packageRevisionCRDName(repo, pkg, workspace string) string {
	return fmt.Sprintf("%s.%s.%s", repo, pkg, workspace)
}

func workspaceRevisionNum(workspace string) int {
	revisionNum := 1
	if strings.Contains(workspace, "v") {
		_, _ = fmt.Sscanf(workspace, "v%d", &revisionNum)
	}
	return revisionNum
}

func deletionCandidateFromFields(name, repoName, packageName, workspaceName string) DeletionCandidate {
	return DeletionCandidate{
		Name:          name,
		RepoName:      repoName,
		PackageName:   packageName,
		WorkspaceName: workspaceName,
		RevisionNum:   workspaceRevisionNum(workspaceName),
	}
}
