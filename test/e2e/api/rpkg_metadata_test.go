// Copyright 2025-2026 The kpt Authors
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

package api

import (
	"fmt"
	"os"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	configMapGVK = corev1.SchemeGroupVersion.WithKind("ConfigMap")
)

type MetadataTestCase struct {
	Name     string
	RepoName string
	Setup    func(*PorchSuite, string) *porchapi.PackageRevision
	Validate func(*PorchSuite, *porchapi.PackageRevision)
}

func (t *PorchSuite) TestPackageRevisionMetadata() {
	cases := []MetadataTestCase{
		{
			Name:     "NewPackageRevisionLabels",
			RepoName: "pkg-rev-labels",
			Setup: func(t *PorchSuite, repoName string) *porchapi.PackageRevision {
				t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
				pr := &porchapi.PackageRevision{
					TypeMeta: metav1.TypeMeta{
						Kind:       "PackageRevision",
						APIVersion: porchapi.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: t.Namespace,
						Labels:    map[string]string{"kpt.dev/label": "foo"},
						Annotations: map[string]string{
							"kpt.dev/anno":       "foo",
							"kpt.dev/other-anno": "bar",
						},
					},
					Spec: porchapi.PackageRevisionSpec{
						PackageName:    "new-package",
						WorkspaceName:  defaultWorkspace,
						RepositoryName: repoName,
						Tasks: []porchapi.Task{{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{Description: "this is a test"},
						}},
					},
				}
				t.CreateF(pr)
				return pr
			},
			Validate: func(t *PorchSuite, pr *porchapi.PackageRevision) {
				t.ValidateLabelsAndAnnos(pr.Name, map[string]string{"kpt.dev/label": "foo"}, map[string]string{"kpt.dev/anno": "foo", "kpt.dev/other-anno": "bar"})
				pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
				t.UpdateF(pr)
				t.GetF(client.ObjectKey{Namespace: pr.Namespace, Name: pr.Name}, pr)

				t.ValidateLabelsAndAnnos(pr.Name, map[string]string{"kpt.dev/label": "foo"}, map[string]string{"kpt.dev/anno": "foo", "kpt.dev/other-anno": "bar"})
				pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
				t.UpdateApprovalF(pr)

				t.ValidateLabelsAndAnnos(pr.Name, map[string]string{"kpt.dev/label": "foo", porchapi.LatestPackageRevisionKey: porchapi.LatestPackageRevisionValue}, map[string]string{"kpt.dev/anno": "foo", "kpt.dev/other-anno": "bar"})
				t.GetF(client.ObjectKey{Namespace: pr.Namespace, Name: pr.Name}, pr)
				delete(pr.Labels, "kpt.dev/label")
				pr.Labels["kpt.dev/other-label"] = "bar"
				delete(pr.Annotations, "kpt.dev/other-anno")
				pr.Spec.Revision = 1
				t.UpdateF(pr)

				t.ValidateLabelsAndAnnos(pr.Name, map[string]string{"kpt.dev/other-label": "bar", porchapi.LatestPackageRevisionKey: porchapi.LatestPackageRevisionValue}, map[string]string{"kpt.dev/anno": "foo"})
				clonedPr := t.CreatePackageSkeleton(pr.Spec.RepositoryName, "cloned-package", defaultWorkspace)
				clonedPr.Spec.Tasks = []porchapi.Task{{
					Type:  porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{Upstream: porchapi.UpstreamPackage{UpstreamRef: &porchapi.PackageRevisionRef{Name: pr.Name}}},
				}}
				t.CreateF(clonedPr)

				t.ValidateLabelsAndAnnos(clonedPr.Name, map[string]string{}, map[string]string{})
			},
		},
		{
			Name:     "RegisteredPackageRevisionLabels",
			RepoName: "test-blueprints",
			Setup: func(t *PorchSuite, repoName string) *porchapi.PackageRevision {
				t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
				var list porchapi.PackageRevisionList
				t.ListE(&list, client.InNamespace(t.Namespace))
				basens := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: repoName}, Package: "basens"}, Revision: 1})
				return basens
			},
			Validate: func(t *PorchSuite, pr *porchapi.PackageRevision) {
				// Validate initial state has no custom labels/annotations
				t.ValidateLabelsAndAnnos(pr.Name, nil, nil)

				// Add labels and annotations
				if pr.Labels == nil {
					pr.Labels = make(map[string]string)
				}
				pr.Labels["kpt.dev/label"] = "foo"
				if pr.Annotations == nil {
					pr.Annotations = make(map[string]string)
				}
				pr.Annotations["kpt.dev/anno"] = "foo"
				t.UpdateF(pr)

				// Validate updated labels and annotations
				t.ValidateLabelsAndAnnos(pr.Name, map[string]string{"kpt.dev/label": "foo"}, map[string]string{"kpt.dev/anno": "foo"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func() {
			pr := tc.Setup(t, tc.RepoName)
			tc.Validate(t, pr)
		})
	}
}

type GarbageCollectionTestCase struct {
	Name     string
	RepoName string
	Test     func(*PorchSuite, string)
}

func (t *PorchSuite) TestPackageRevisionGarbageCollection() {
	cases := []GarbageCollectionTestCase{
		{
			Name:     "PackageRevisionDeletesCascadesToOwnedObjects",
			RepoName: "pkgrevgcwithowner",
			Test: func(t *PorchSuite, repoName string) {
				t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
				pr := t.CreatePackageDraftF(repoName, "empty-package", "test-workspace")
				cm := &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{Kind: configMapGVK.Kind, APIVersion: configMapGVK.GroupVersion().String()},
					ObjectMeta: metav1.ObjectMeta{Name: "cm-cascades", Namespace: t.Namespace, OwnerReferences: []metav1.OwnerReference{
						{APIVersion: porchapi.SchemeGroupVersion.String(), Kind: suiteutils.PackageRevisionGVK.Kind, Name: pr.Name, UID: pr.UID}}},
					Data: map[string]string{"foo": "bar"},
				}
				t.CreateF(cm)
				t.DeleteF(pr)
				t.WaitUntilObjectDeleted(suiteutils.PackageRevisionGVK, types.NamespacedName{Name: pr.Name, Namespace: pr.Namespace}, 10*time.Second)
				t.WaitUntilObjectDeleted(configMapGVK, types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}, 10*time.Second)
			},
		},
		{
			Name:     "PackageRevisionCanHaveOwnerReferences",
			RepoName: "pkgrevownerrefs",
			Test: func(t *PorchSuite, repoName string) {
				t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
				cm := &corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: configMapGVK.Kind, APIVersion: configMapGVK.GroupVersion().String()},
					ObjectMeta: metav1.ObjectMeta{Name: "cm-ownerrefs", Namespace: t.Namespace},
					Data:       map[string]string{"foo": "bar"},
				}
				t.CreateF(cm)
				pr := t.CreatePackageDraftF(repoName, "empty-package", "test-workspace")
				t.ValidateOwnerReferences(pr.Name, []metav1.OwnerReference{})
				ownerRef := metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: cm.Name, UID: cm.UID}
				pr.OwnerReferences = []metav1.OwnerReference{ownerRef}
				t.UpdateF(pr)
				t.ValidateOwnerReferences(pr.Name, []metav1.OwnerReference{ownerRef})
			},
		},
		{
			Name:     "PackageRevisionDeletedWhenOwnerDeleted",
			RepoName: "pkgrevgcasowner",
			Test: func(t *PorchSuite, repoName string) {
				if _, ok := os.LookupEnv("DB_CACHE"); ok {
					return
				}
				t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
				cm := &corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: configMapGVK.Kind, APIVersion: configMapGVK.GroupVersion().String()},
					ObjectMeta: metav1.ObjectMeta{Name: "cm-ownerdeleted", Namespace: t.Namespace},
					Data:       map[string]string{"foo": "bar"},
				}
				t.CreateF(cm)
				pr := &porchapi.PackageRevision{
					TypeMeta:   metav1.TypeMeta{Kind: suiteutils.PackageRevisionGVK.Kind, APIVersion: suiteutils.PackageRevisionGVK.GroupVersion().String()},
					ObjectMeta: metav1.ObjectMeta{Namespace: t.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: cm.Name, UID: cm.UID}}},
					Spec:       porchapi.PackageRevisionSpec{PackageName: "empty-package", WorkspaceName: "test-workspace", RepositoryName: repoName},
				}
				t.CreateF(pr)
				t.DeleteF(cm)
				t.WaitUntilObjectDeleted(configMapGVK, types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}, 20*time.Second)
				t.WaitUntilObjectDeleted(suiteutils.PackageRevisionGVK, types.NamespacedName{Name: pr.Name, Namespace: pr.Namespace}, 20*time.Second)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func() {
			tc.Test(t, tc.RepoName)
		})
	}
}

type FieldSelectorTestCase struct {
	Name           string
	FieldSelector  fields.Set
	ExpectResults  bool
	ValidateResult func(*porchapi.PackageRevision) error
}

func (t *PorchSuite) TestPackageRevisionFieldSelectors() {
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cases := []FieldSelectorTestCase{
		{
			Name:          "PackageName",
			FieldSelector: fields.Set{"spec.packageName": "basens"},
			ExpectResults: true,
			ValidateResult: func(pr *porchapi.PackageRevision) error {
				if pr.Spec.PackageName != "basens" {
					return fmt.Errorf("PackageRevision %s packageName: want %q, but got %q", pr.Name, "basens", pr.Spec.PackageName)
				}
				return nil
			},
		},
		{
			Name:          "Revision",
			FieldSelector: fields.Set{"spec.revision": "1"},
			ExpectResults: true,
			ValidateResult: func(pr *porchapi.PackageRevision) error {
				if pr.Spec.Revision != 1 {
					return fmt.Errorf("PackageRevision %s revision: want %d, but got %d", pr.Name, 1, pr.Spec.Revision)
				}
				return nil
			},
		},
		{
			Name:          "WorkspaceName",
			FieldSelector: fields.Set{"spec.workspaceName": "v1"},
			ExpectResults: true,
			ValidateResult: func(pr *porchapi.PackageRevision) error {
				if pr.Spec.WorkspaceName != "v1" {
					return fmt.Errorf("PackageRevision %s workspaceName: want %q, but got %q", pr.Name, "v1", pr.Spec.WorkspaceName)
				}
				return nil
			},
		},
		{
			Name:          "LifecyclePublished",
			FieldSelector: fields.Set{"spec.lifecycle": string(porchapi.PackageRevisionLifecyclePublished)},
			ExpectResults: true,
			ValidateResult: func(pr *porchapi.PackageRevision) error {
				if pr.Spec.Lifecycle != porchapi.PackageRevisionLifecyclePublished {
					return fmt.Errorf("PackageRevision %s lifecycle: want %q, but got %q", pr.Name, porchapi.PackageRevisionLifecyclePublished, pr.Spec.Lifecycle)
				}
				return nil
			},
		},
		{
			Name:          "LifecycleDraft",
			FieldSelector: fields.Set{"spec.lifecycle": string(porchapi.PackageRevisionLifecycleDraft)},
			ExpectResults: false,
			ValidateResult: func(pr *porchapi.PackageRevision) error {
				if pr.Spec.Lifecycle != porchapi.PackageRevisionLifecycleDraft {
					return fmt.Errorf("PackageRevision %s lifecycle: want %q, but got %q", pr.Name, porchapi.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle)
				}
				return nil
			},
		},
		{
			Name:          "CombinedSelectors",
			FieldSelector: fields.Set{"spec.revision": "1", "spec.packageName": "basens"},
			ExpectResults: true,
			ValidateResult: func(pr *porchapi.PackageRevision) error {
				if pr.Spec.PackageName != "basens" || pr.Spec.Revision != 1 {
					return fmt.Errorf("PackageRevision %s: want basens/1, but got %v/%v", pr.Name, pr.Spec.PackageName, pr.Spec.Revision)
				}
				return nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func() {
			var prList porchapi.PackageRevisionList
			selector := client.MatchingFields(tc.FieldSelector)
			t.ListE(&prList, client.InNamespace(t.Namespace), selector)

			if tc.ExpectResults && len(prList.Items) == 0 {
				t.Errorf("Expected at least one PackageRevision with selector %v, but got none", tc.FieldSelector)
				return
			}

			for _, pr := range prList.Items {
				if err := tc.ValidateResult(&pr); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

type LabelSelectorTestCase struct {
	Name          string
	LabelSelector labels.Set
	ExpectedCount int
}

func (t *PorchSuite) TestPackageRevisionLabelSelectors() {
	const (
		repository = "pkg-rev-label-selectors"
		labelKey   = "kpt.dev/label"
		labelVal1  = "foo"
		labelVal2  = "bar"
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	pr := porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchapi.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
			Labels:    map[string]string{labelKey: labelVal1},
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    "new-package",
			WorkspaceName:  defaultWorkspace,
			RepositoryName: repository,
			Tasks: []porchapi.Task{{
				Type: porchapi.TaskTypeInit,
				Init: &porchapi.PackageInitTaskSpec{Description: "this is a test"},
			}},
		},
	}
	t.CreateF(&pr)

	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(&pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(&pr)

	cases := []LabelSelectorTestCase{
		{
			Name:          "ExistingLabel",
			LabelSelector: labels.Set{labelKey: labelVal1},
			ExpectedCount: 1,
		},
		{
			Name:          "NonExistentLabel",
			LabelSelector: labels.Set{labelKey: labelVal2},
			ExpectedCount: 0,
		},
		{
			Name:          "LatestPackageRevisionLabel",
			LabelSelector: labels.Set{porchapi.LatestPackageRevisionKey: porchapi.LatestPackageRevisionValue},
			ExpectedCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func() {
			var prList porchapi.PackageRevisionList
			pkgSelector := client.MatchingLabels(tc.LabelSelector)
			t.ListE(&prList, client.InNamespace(t.Namespace), pkgSelector)
			require.Equal(t.T(), tc.ExpectedCount, len(prList.Items))
		})
	}
}

func (t *PorchSuite) TestPackageRevisionFinalizers() {
	const (
		repository = "pkgrevfinalizers"
		workspace  = "pkgrevfinalizers-workspace"
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	pr := t.CreatePackageDraftF(repository, "empty-package", workspace)
	t.ValidateFinalizers(pr.Name, []string{})

	readPR := t.GetPackageRevision(repository, "empty-package", 0)
	readPR.Finalizers = append(readPR.Finalizers, "foo-finalizer")
	t.UpdateF(readPR)
	t.ValidateFinalizers(readPR.Name, []string{"foo-finalizer"})

	readPR = t.GetPackageRevision(repository, "empty-package", 0)
	t.DeleteF(readPR)
	t.ValidateFinalizers(readPR.Name, []string{"foo-finalizer"})

	readPR = t.GetPackageRevision(repository, "empty-package", 0)
	readPR.Finalizers = []string{}
	t.UpdateF(readPR)
	t.WaitUntilObjectDeleted(suiteutils.PackageRevisionGVK, types.NamespacedName{Name: readPR.Name, Namespace: readPR.Namespace}, 10*time.Second)
}
