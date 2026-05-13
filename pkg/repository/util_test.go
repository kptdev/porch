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

package repository

import (
	"context"
	"fmt"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

func TestRevision2Int(t *testing.T) {
	assert.Equal(t, 123, Revision2Int("123"))
	assert.Equal(t, 123, Revision2Int("v123"))
	assert.Equal(t, -1, Revision2Int("V123"))
	assert.Equal(t, -1, Revision2Int("v123v"))
	assert.Equal(t, -1, Revision2Int("-1"))
	assert.Equal(t, 0, Revision2Int("v0"))
	assert.Equal(t, 0, Revision2Int("0"))
}

func TestRevision2Str(t *testing.T) {
	assert.Equal(t, "123", Revision2Str(123))
	assert.Equal(t, "-1", Revision2Str(-1))
}

func TestComposePkgObjName(t *testing.T) {
	pkgKey := PackageKey{
		RepoKey: RepositoryKey{
			Namespace:         "the-ns",
			Name:              "the-repo",
			Path:              "the/dir/path",
			PlaceholderWSname: "the-placeholder-ws-name",
		},
		Path:    "the/pkg/path",
		Package: "the-package-name",
	}

	assert.Equal(t, "the-repo.the.pkg.path.the-package-name", ComposePkgObjName(pkgKey))

	pkgKey.Path = ""
	assert.Equal(t, "the-repo.the-package-name", ComposePkgObjName(pkgKey))
}

func TestComposePkgRevObjName(t *testing.T) {
	pkgRevKey := PackageRevisionKey{
		PkgKey: PackageKey{
			RepoKey: RepositoryKey{
				Namespace:         "the-ns",
				Name:              "the-repo",
				Path:              "the/dir/path",
				PlaceholderWSname: "the-placeholder-ws-name",
			},
			Path:    "the/pkg/path",
			Package: "the-package-name",
		},
		Revision:      123,
		WorkspaceName: "the-ws-name",
	}

	assert.Equal(t, "the-repo.the.pkg.path.the-package-name.the-ws-name", ComposePkgRevObjName(pkgRevKey))

	pkgRevKey.Revision = -1
	assert.Equal(t, "the-repo.the.pkg.path.the-package-name.the-ws-name", ComposePkgRevObjName(pkgRevKey))
}

func TestToAPIReadinessGate(t *testing.T) {
	kf := kptfilev1.KptFile{}

	readinessGates := ToAPIReadinessGates(kf)
	assert.Equal(t, 0, len(readinessGates))

	kf.Info = &kptfilev1.PackageInfo{
		ReadinessGates: []kptfilev1.ReadinessGate{},
	}
	readinessGates = ToAPIReadinessGates(kf)
	assert.Equal(t, 0, len(readinessGates))

	kf.Info.ReadinessGates = append(kf.Info.ReadinessGates, kptfilev1.ReadinessGate{
		ConditionType: "AConditionType",
	})
	readinessGates = ToAPIReadinessGates(kf)
	assert.Equal(t, 1, len(readinessGates))
}

func TestToAPIConditions(t *testing.T) {
	kf := kptfilev1.KptFile{}

	conditions := ToAPIConditions(kf)
	assert.Equal(t, 0, len(conditions))

	kf.Status = &kptfilev1.Status{
		Conditions: []kptfilev1.Condition{},
	}
	conditions = ToAPIConditions(kf)
	assert.Equal(t, 0, len(conditions))

	kf.Status.Conditions = append(kf.Status.Conditions, kptfilev1.Condition{
		Type:    "AConditionType",
		Status:  kptfilev1.ConditionTrue,
		Reason:  "A Reason",
		Message: "A Message",
	})
	conditions = ToAPIConditions(kf)
	assert.Equal(t, 1, len(conditions))
}

func TestToAPIConditionStatus(t *testing.T) {
	assert.Equal(t, porchapi.ConditionTrue, toAPIConditionStatus(kptfilev1.ConditionTrue))
	assert.Equal(t, porchapi.ConditionFalse, toAPIConditionStatus(kptfilev1.ConditionFalse))
	assert.Equal(t, porchapi.ConditionUnknown, toAPIConditionStatus(kptfilev1.ConditionUnknown))
}

func TestUpsertAPICondition(t *testing.T) {
	conditions := []porchapi.Condition{}

	condition0 := porchapi.Condition{
		Type: "Condition0",
	}

	upsertedConditions := UpsertAPICondition(conditions, condition0)
	assert.Equal(t, 1, len(upsertedConditions))
	assert.Equal(t, condition0, upsertedConditions[0])

	upsertedConditions = UpsertAPICondition(upsertedConditions, condition0)
	assert.Equal(t, 1, len(upsertedConditions))
	assert.Equal(t, condition0, upsertedConditions[0])

	condition1 := porchapi.Condition{
		Type: "Condition1",
	}

	upsertedConditions = UpsertAPICondition(upsertedConditions, condition1)
	assert.Equal(t, 2, len(upsertedConditions))
	assert.Equal(t, condition0, upsertedConditions[0])
	assert.Equal(t, condition1, upsertedConditions[1])

	upsertedConditions = UpsertAPICondition(upsertedConditions, condition0)
	assert.Equal(t, 2, len(upsertedConditions))
	assert.Equal(t, condition0, upsertedConditions[1])
	assert.Equal(t, condition1, upsertedConditions[0])
}

func TestAnyBlockOwnerDeletionSet(t *testing.T) {
	assert.False(t, AnyBlockOwnerDeletionSet(metav1.ObjectMeta{}))

	meta := metav1.ObjectMeta{
		OwnerReferences: []metav1.OwnerReference{},
	}
	assert.False(t, AnyBlockOwnerDeletionSet(meta))

	ownerRef := metav1.OwnerReference{}
	meta.OwnerReferences = append(meta.OwnerReferences, ownerRef)
	assert.True(t, AnyBlockOwnerDeletionSet(meta))

	blockOwnerDeletion := false
	meta.OwnerReferences[0].BlockOwnerDeletion = &blockOwnerDeletion
	assert.False(t, AnyBlockOwnerDeletionSet(meta))

	blockOwnerDeletion = true
	assert.True(t, AnyBlockOwnerDeletionSet(meta))
}

func TestTestPrSliceToMap(t *testing.T) {
	assert.Equal(t, 0, len(PrSlice2Map([]PackageRevision{})))

	prSlice := []PackageRevision{}

	assert.Equal(t, 0, len(PrSlice2Map(prSlice)))
}

func TestKptUpstreamLock2APIUpstreamLock(t *testing.T) {
	kptLock := kptfilev1.Locator{}
	assert.Nil(t, KptUpstreamLock2APIUpstreamLock(kptLock).Git)

	kptLock.Git = &kptfilev1.GitLock{
		Repo: "my-repo",
	}

	assert.Equal(t, "my-repo", KptUpstreamLock2APIUpstreamLock(kptLock).Git.Repo)
}

func TestKptUpstreamLock2KptUpstream(t *testing.T) {
	kptLock := kptfilev1.Locator{}
	assert.Nil(t, KptUpstreamLock2KptUpstream(kptLock).Git)

	kptLock.Git = &kptfilev1.GitLock{
		Repo: "my-repo",
	}

	assert.Equal(t, "my-repo", KptUpstreamLock2KptUpstream(kptLock).Git.Repo)
}

func TestPathsOverlap(t *testing.T) {
	assert.False(t, PathsOverlap("pkg1", "pkg1"))
	assert.True(t, PathsOverlap("pkg", "pkg/sub"))
	assert.True(t, PathsOverlap("pkg/sub", "pkg"))
	assert.False(t, PathsOverlap("pkg1", "pkg2"))
	assert.False(t, PathsOverlap("pkg", "pkg-other"))
}

func TestValidatePackagePathOverlap(t *testing.T) {
	newPr := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    "new-pkg",
			RepositoryName: "repo1",
		},
	}

	assert.NoError(t, ValidatePackagePathOverlap(newPr, []PackageRevision{}))

	samePackageNameRepoRev := &fakePackageRevision{
		repoName:    "repo1",
		packageName: "new-pkg",
	}
	err := ValidatePackagePathOverlap(newPr, []PackageRevision{samePackageNameRepoRev})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	overlappingPathRev := &fakePackageRevision{
		repoName:    "repo1",
		packageName: "new",
	}
	newPr.Spec.PackageName = "new/sub"
	err = ValidatePackagePathOverlap(newPr, []PackageRevision{overlappingPathRev})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts")

	differentRepoRev := &fakePackageRevision{
		repoName:    "repo2",
		packageName: "new",
	}
	assert.NoError(t, ValidatePackagePathOverlap(newPr, []PackageRevision{differentRepoRev}))
}

func TestWriteResourcesToFS(t *testing.T) {
	tests := []struct {
		name       string
		rootDir    string
		resources  map[string]string
		wantPkgDir string
		wantFiles  map[string]string
	}{
		{
			name:       "empty resources",
			resources:  map[string]string{},
			wantPkgDir: "",
		},
		{
			name:       "single file at root",
			resources:  map[string]string{"foo.yaml": "content"},
			wantPkgDir: "",
			wantFiles:  map[string]string{"/foo.yaml": "content"},
		},
		{
			name:       "Kptfile at root",
			resources:  map[string]string{"Kptfile": "kpt-content"},
			wantPkgDir: "/",
			wantFiles:  map[string]string{"/Kptfile": "kpt-content"},
		},
		{
			name:       "nested file",
			resources:  map[string]string{"sub/dir/file.yaml": "nested"},
			wantPkgDir: "",
			wantFiles:  map[string]string{"/sub/dir/file.yaml": "nested"},
		},
		{
			name:       "Kptfile in subdir",
			resources:  map[string]string{"pkg/Kptfile": "kpt"},
			wantPkgDir: "pkg",
			wantFiles:  map[string]string{"/pkg/Kptfile": "kpt"},
		},
		{
			name: "Kptfile at root takes precedence over nested",
			resources: map[string]string{
				"Kptfile":       "root-kpt",
				"sub/Kptfile":   "sub-kpt",
				"sub/file.yaml": "data",
			},
			wantPkgDir: "/",
			wantFiles: map[string]string{
				"/Kptfile":       "root-kpt",
				"/sub/Kptfile":   "sub-kpt",
				"/sub/file.yaml": "data",
			},
		},
		{
			name:       "with rootDir",
			rootDir:    "root",
			resources:  map[string]string{"Kptfile": "kpt", "file.yaml": "data"},
			wantPkgDir: "/",
			wantFiles: map[string]string{
				"/root/Kptfile":   "kpt",
				"/root/file.yaml": "data",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := filesys.MakeFsInMemory()
			gotPkgDir, err := WriteResourcesToFS(fs, tt.rootDir, tt.resources)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantPkgDir, gotPkgDir)

			for path, wantContent := range tt.wantFiles {
				data, err := fs.ReadFile(path)
				assert.NoError(t, err, "reading %s", path)
				assert.Equal(t, wantContent, string(data))
			}
		})
	}
}

type fakeReferenceResolver struct {
	repo *configapi.Repository
	err  error
}

func (f *fakeReferenceResolver) ResolveReference(_ context.Context, _, _ string, result Object) error {
	if f.err != nil {
		return f.err
	}
	if repo, ok := result.(*configapi.Repository); ok {
		*repo = *f.repo
	}
	return nil
}

func TestPackageRevisionIsPlaceholder(t *testing.T) {
	tests := []struct {
		name     string
		pr       PackageRevision
		resolver ReferenceResolver
		want     bool
		wantErr  bool
	}{
		{
			name: "published revision is not placeholder",
			pr:   &fakePackageRevision{revision: 1, workspaceName: "main", repoName: "repo"},
			want: false,
		},
		{
			name: "draft with matching git branch is placeholder",
			pr:   &fakePackageRevision{revision: -1, workspaceName: "main", repoName: "repo"},
			resolver: &fakeReferenceResolver{repo: &configapi.Repository{
				Spec: configapi.RepositorySpec{Git: &configapi.GitRepository{Branch: "main"}},
			}},
			want: true,
		},
		{
			name: "draft with non-matching git branch is not placeholder",
			pr:   &fakePackageRevision{revision: -1, workspaceName: "dev", repoName: "repo"},
			resolver: &fakeReferenceResolver{repo: &configapi.Repository{
				Spec: configapi.RepositorySpec{Git: &configapi.GitRepository{Branch: "main"}},
			}},
			want: false,
		},
		{
			name: "draft with no git config is not placeholder",
			pr:   &fakePackageRevision{revision: -1, workspaceName: "main", repoName: "repo"},
			resolver: &fakeReferenceResolver{repo: &configapi.Repository{
				Spec: configapi.RepositorySpec{},
			}},
			want: false,
		},
		{
			name:     "resolver error returns error",
			pr:       &fakePackageRevision{revision: -1, workspaceName: "main", repoName: "repo"},
			resolver: &fakeReferenceResolver{err: fmt.Errorf("not found")},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := tt.resolver
			if resolver == nil {
				resolver = &fakeReferenceResolver{repo: &configapi.Repository{}}
			}
			got, err := PackageRevisionIsPlaceholder(context.Background(), "ns", resolver, tt.pr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
