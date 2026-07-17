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

	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type v1alpha1Driver struct {
	baseDriver
}

func (d *v1alpha1Driver) Version() PorchAPIVersion {
	return PorchAPIV1Alpha1
}

func (d *v1alpha1Driver) RepositoryAnnotations() map[string]string {
	return nil
}

func (d *v1alpha1Driver) DoLifecycle(repoName, pkgName string, revisionNum int) (string, error) {
	t := d.suite
	var list porchv1alpha1.PackageRevisionList
	var taskList []porchv1alpha1.Task

	t.initPkgRevMetrics(repoName, pkgName, revisionNum)

	start := time.Now()
	err := retry.RetryOnConflict(retryBackoff, func() error {
		return t.client.List(t.ctx, &list, client.InNamespace(t.testOptions.namespace))
	})
	d.recordListOperation(repoName, pkgName, revisionNum, start, err)
	if err != nil {
		return "", err
	}

	var latestPR *porchv1alpha1.PackageRevision
	for i := range list.Items {
		pr := &list.Items[i]
		if pr.Spec.PackageName == pkgName &&
			pr.Spec.RepositoryName == repoName &&
			pr.Spec.Lifecycle == porchv1alpha1.PackageRevisionLifecyclePublished {
			if latestPR == nil || pr.Spec.Revision > latestPR.Spec.Revision {
				latestPR = pr
			}
		}
	}

	if revisionNum == 1 {
		taskList = []porchv1alpha1.Task{
			{
				Type: porchv1alpha1.TaskTypeInit,
				Init: &porchv1alpha1.PackageInitTaskSpec{
					Description: fmt.Sprintf("Test package %s for Porch metrics", pkgName),
					Keywords:    []string{"test", "metrics"},
					Site:        "https://kpt.dev/",
				},
			},
		}
		if t.enablePrometheus {
			telemetry.PerfTestIncrementPackageCounter()
		}
	} else if latestPR != nil {
		taskList = []porchv1alpha1.Task{
			{
				Type: porchv1alpha1.TaskTypeEdit,
				Edit: &porchv1alpha1.PackageEditTaskSpec{
					Source: &porchv1alpha1.PackageRevisionRef{
						Name: latestPR.Name,
					},
				},
			},
		}
	}

	workspace := fmt.Sprintf("v%d", revisionNum)
	pkgRev := &porchv1alpha1.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.testOptions.namespace,
		},
		Spec: porchv1alpha1.PackageRevisionSpec{
			PackageName:    pkgName,
			WorkspaceName:  workspace,
			RepositoryName: repoName,
			Tasks:          taskList,
		},
	}

	if err = d.createPackageRevision(pkgRev, repoName, pkgName, revisionNum); err != nil {
		return "", err
	}

	if err = d.updatePackageRevisionResources(repoName, pkgName, pkgRev.Name, revisionNum); err != nil {
		return "", err
	}

	pkg := &porchv1alpha1.PackageRevision{}
	if err = d.proposePackage(
		repoName, pkgName, pkgRev.Name, revisionNum,
		pkg,
		func(obj client.Object) string { return string(obj.(*porchv1alpha1.PackageRevision).Spec.Lifecycle) },
		func(obj client.Object) error {
			pr := obj.(*porchv1alpha1.PackageRevision)
			pr.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleProposed
			return t.client.Update(t.ctx, pr)
		},
		string(porchv1alpha1.PackageRevisionLifecycleProposed),
	); err != nil {
		return "", err
	}

	if err = d.approvePackage(
		repoName, pkgName, pkgRev.Name, revisionNum,
		pkg,
		func(obj client.Object) error {
			pr := obj.(*porchv1alpha1.PackageRevision)
			pr.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecyclePublished
			_, err := t.clientSet.PorchV1alpha1().PackageRevisions(t.testOptions.namespace).UpdateApproval(t.ctx, pkgRev.Name, pr, metav1.UpdateOptions{})
			return err
		},
		string(porchv1alpha1.PackageRevisionLifecycleProposed),
		string(porchv1alpha1.PackageRevisionLifecyclePublished),
	); err != nil {
		return "", err
	}

	return pkgRev.Name, nil
}

func (d *v1alpha1Driver) DeletePackageRevision(repoName, pkgName, pkgRevName string, revisionNum int) error {
	return d.deletePackageRevision(
		repoName, pkgName, pkgRevName, revisionNum,
		func() client.Object { return &porchv1alpha1.PackageRevision{} },
		func(obj client.Object) string {
			return string(obj.(*porchv1alpha1.PackageRevision).Spec.Lifecycle)
		},
		func(obj client.Object) error {
			pkgRev := obj.(*porchv1alpha1.PackageRevision)
			pkgRev.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleDeletionProposed
			return d.suite.client.Update(d.suite.ctx, pkgRev)
		},
		deletionBehavior{
			alwaysProposeDeletion: true,
			deletionProposed:      string(porchv1alpha1.PackageRevisionLifecycleDeletionProposed),
		},
	)
}

func (d *v1alpha1Driver) ListPackageRevisionsForDeletion() ([]DeletionCandidate, error) {
	return d.listPackageRevisionsForDeletion(&porchv1alpha1.PackageRevisionList{}, func(obj client.Object) (DeletionCandidate, bool) {
		pr, ok := obj.(*porchv1alpha1.PackageRevision)
		if !ok {
			return DeletionCandidate{}, false
		}
		return deletionCandidateFromFields(pr.Name, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName), true
	})
}
