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
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/internal/telemetry"
	pkgerrors "github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const v1alpha2ControllerWaitTimeout = 120 * time.Second

type v1alpha2Driver struct {
	baseDriver
}

func (d *v1alpha2Driver) Version() PorchAPIVersion {
	return PorchAPIV1Alpha2
}

func (d *v1alpha2Driver) RepositoryAnnotations() map[string]string {
	return map[string]string{
		"porch.kpt.dev/v1alpha2-migration": "true",
	}
}

func (d *v1alpha2Driver) DoLifecycle(repoName, pkgName string, revisionNum int) (string, error) {
	t := d.suite
	var list porchv1alpha2.PackageRevisionList

	t.initPkgRevMetrics(repoName, pkgName, revisionNum)

	start := time.Now()
	err := retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.List(t.ctx, &list, client.InNamespace(t.testOptions.namespace))
	})
	d.recordListOperation(repoName, pkgName, revisionNum, start, err)
	if err != nil {
		return "", err
	}

	var latestPR *porchv1alpha2.PackageRevision
	for i := range list.Items {
		pr := &list.Items[i]
		if pr.Spec.PackageName == pkgName &&
			pr.Spec.RepositoryName == repoName &&
			pr.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished {
			if latestPR == nil || pr.Status.Revision > latestPR.Status.Revision {
				latestPR = pr
			}
		}
	}

	workspace := fmt.Sprintf("v%d", revisionNum)
	pkgRev := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      packageRevisionCRDName(repoName, pkgName, workspace),
			Namespace: t.testOptions.namespace,
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    pkgName,
			RepositoryName: repoName,
			WorkspaceName:  workspace,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	if revisionNum == 1 {
		pkgRev.Spec.Source = &porchv1alpha2.PackageSource{
			Init: &porchv1alpha2.PackageInitSpec{
				Description: fmt.Sprintf("Test package %s for Porch metrics", pkgName),
				Keywords:    []string{"test", "metrics"},
				Site:        "https://kpt.dev/",
			},
		}
		if t.enablePrometheus {
			telemetry.PerfTestIncrementPackageCounter()
		}
	} else if latestPR != nil {
		pkgRev.Spec.Source = &porchv1alpha2.PackageSource{
			CopyFrom: &porchv1alpha2.PackageRevisionRef{
				Name: latestPR.Name,
			},
		}
	}

	if err = d.createPackageRevision(pkgRev, repoName, pkgName, revisionNum); err != nil {
		return "", err
	}

	if err = d.waitForReady(pkgRev, repoName, pkgName, revisionNum); err != nil {
		return "", err
	}

	if err = d.updatePackageRevisionResources(repoName, pkgName, pkgRev.Name, revisionNum); err != nil {
		return "", err
	}

	if err = d.waitForRendered(pkgRev, repoName, pkgName, revisionNum); err != nil {
		return "", err
	}

	if err = d.proposePackage(
		repoName, pkgName, pkgRev.Name, revisionNum,
		pkgRev,
		func(obj client.Object) string { return string(obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle) },
		func(obj client.Object) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
			return t.client.Update(t.ctx, pr)
		},
		string(porchv1alpha2.PackageRevisionLifecycleProposed),
	); err != nil {
		return "", err
	}

	if err = d.waitForReady(pkgRev, repoName, pkgName, revisionNum); err != nil {
		return "", err
	}

	if err = d.approvePackage(
		repoName, pkgName, pkgRev.Name, revisionNum,
		pkgRev,
		func(obj client.Object) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return t.client.Update(t.ctx, pr)
		},
		string(porchv1alpha2.PackageRevisionLifecycleProposed),
		string(porchv1alpha2.PackageRevisionLifecyclePublished),
	); err != nil {
		return "", err
	}

	if err = d.waitForPublished(pkgRev, repoName, pkgName, revisionNum); err != nil {
		return "", err
	}

	return pkgRev.Name, nil
}

func (d *v1alpha2Driver) waitForReady(pkgRev *porchv1alpha2.PackageRevision, repoName, pkgName string, revisionNum int) error {
	start := time.Now()
	err := d.pollUntil(v1alpha2ControllerWaitTimeout, func() (bool, error) {
		if err := d.suite.ctx.Err(); err != nil {
			return false, err
		}
		if err := d.suite.client.Get(d.suite.ctx, client.ObjectKeyFromObject(pkgRev), pkgRev); err != nil {
			return false, err
		}
		if pkgRev.Status.RenderingPrrResourceVersion != "" {
			return false, nil
		}
		for _, cond := range pkgRev.Status.Conditions {
			if cond.Type == porchv1alpha2.ConditionReady &&
				cond.Status == metav1.ConditionTrue &&
				cond.ObservedGeneration == pkgRev.Generation {
				return true, nil
			}
		}
		return false, nil
	})
	d.recordPkgRevOperation(repoName, pkgName, revisionNum, pkgRevWaitReady, start, err)
	return err
}

func (d *v1alpha2Driver) waitForRendered(pkgRev *porchv1alpha2.PackageRevision, repoName, pkgName string, revisionNum int) error {
	start := time.Now()
	err := d.pollUntil(v1alpha2ControllerWaitTimeout, func() (bool, error) {
		if err := d.suite.ctx.Err(); err != nil {
			return false, err
		}
		if err := d.suite.client.Get(d.suite.ctx, client.ObjectKeyFromObject(pkgRev), pkgRev); err != nil {
			return false, err
		}
		renderReq := pkgRev.Annotations[porchv1alpha2.AnnotationRenderRequest]
		if renderReq != "" && pkgRev.Status.ObservedPrrResourceVersion != renderReq {
			return false, nil
		}
		for _, cond := range pkgRev.Status.Conditions {
			if cond.Type == porchv1alpha2.ConditionRendered && cond.Status == metav1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	d.recordPkgRevOperation(repoName, pkgName, revisionNum, pkgRevWaitRendered, start, err)
	return err
}

func (d *v1alpha2Driver) waitForPublished(pkgRev *porchv1alpha2.PackageRevision, repoName, pkgName string, revisionNum int) error {
	start := time.Now()
	err := d.pollUntil(v1alpha2ControllerWaitTimeout, func() (bool, error) {
		if err := d.suite.ctx.Err(); err != nil {
			return false, err
		}
		if err := d.suite.client.Get(d.suite.ctx, client.ObjectKeyFromObject(pkgRev), pkgRev); err != nil {
			return false, err
		}
		return pkgRev.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished && pkgRev.Status.Revision != 0, nil
	})
	d.recordPkgRevOperation(repoName, pkgName, revisionNum, pkgRevWaitPublished, start, err)
	return err
}

func (d *v1alpha2Driver) pollUntil(timeout time.Duration, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return pkgerrors.Errorf("timeout after %v waiting for controller reconciliation", timeout)
		}
		select {
		case <-d.suite.ctx.Done():
			return d.suite.ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (d *v1alpha2Driver) waitUntilReady(pkgRev *porchv1alpha2.PackageRevision) error {
	t := d.suite
	return d.pollUntil(v1alpha2ControllerWaitTimeout, func() (bool, error) {
		if err := t.ctx.Err(); err != nil {
			return false, err
		}
		if err := t.client.Get(t.ctx, client.ObjectKeyFromObject(pkgRev), pkgRev); err != nil {
			return false, err
		}
		if pkgRev.Status.RenderingPrrResourceVersion != "" {
			return false, nil
		}
		for _, cond := range pkgRev.Status.Conditions {
			if cond.Type == porchv1alpha2.ConditionReady &&
				cond.Status == metav1.ConditionTrue &&
				cond.ObservedGeneration == pkgRev.Generation {
				return true, nil
			}
		}
		return false, nil
	})
}

func (d *v1alpha2Driver) waitForDeleted(pkgRevName string) error {
	t := d.suite
	return d.pollUntil(v1alpha2ControllerWaitTimeout, func() (bool, error) {
		if err := t.ctx.Err(); err != nil {
			return false, err
		}
		obj := &porchv1alpha2.PackageRevision{}
		err := t.client.Get(t.ctx, client.ObjectKey{Namespace: t.testOptions.namespace, Name: pkgRevName}, obj)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})
}

func (d *v1alpha2Driver) DeletePackageRevision(repoName, pkgName, pkgRevName string, revisionNum int) error {
	return d.deletePackageRevision(
		repoName, pkgName, pkgRevName, revisionNum,
		func() client.Object { return &porchv1alpha2.PackageRevision{} },
		func(obj client.Object) string {
			return string(obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle)
		},
		func(obj client.Object) error {
			pkgRev := obj.(*porchv1alpha2.PackageRevision)
			pkgRev.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
			return d.suite.client.Update(d.suite.ctx, pkgRev)
		},
		deletionBehavior{
			publishedLifecycle: string(porchv1alpha2.PackageRevisionLifecyclePublished),
			deletionProposed:   string(porchv1alpha2.PackageRevisionLifecycleDeletionProposed),
			afterProposeDeletion: func(repoName, pkgName, pkgRevName string, revisionNum int) error {
				pr := &porchv1alpha2.PackageRevision{}
				if err := d.suite.client.Get(d.suite.ctx, client.ObjectKey{Namespace: d.suite.testOptions.namespace, Name: pkgRevName}, pr); err != nil {
					return err
				}
				return d.waitUntilReady(pr)
			},
			waitForDeleted: d.waitForDeleted,
		},
	)
}

func (d *v1alpha2Driver) ListPackageRevisionsForDeletion() ([]DeletionCandidate, error) {
	return d.listPackageRevisionsForDeletion(&porchv1alpha2.PackageRevisionList{}, func(obj client.Object) (DeletionCandidate, bool) {
		pr, ok := obj.(*porchv1alpha2.PackageRevision)
		if !ok {
			return DeletionCandidate{}, false
		}
		return deletionCandidateFromFields(pr.Name, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName), true
	})
}
