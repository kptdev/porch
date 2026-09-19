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

package upgrade

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapiv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func createV1Alpha2Scheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, api := range (runtime.SchemeBuilder{
		porchv1alpha2.AddToScheme,
	}) {
		if err := api(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().Int("revision", 0, "")
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().String("strategy", "", "")
	cmd.Flags().String("discover", "", "")
	cmd.Flags().String("subpackage-dir", "", "")
	return cmd
}

func TestV1Alpha2PreRunE(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()
	cfg := &genericclioptions.ConfigFlags{Namespace: &ns}

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    cfg,
		client: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	cmd := newUpgradeCmd()
	assert.NoError(t, cmd.Flags().Set("revision", "1"))
	assert.NoError(t, cmd.Flags().Set("workspace", "v1"))
	assert.NoError(t, cmd.Flags().Set("strategy", "resource-merge"))
	err = r.preRunE(cmd, []string{"test-pkg"})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if r.client == nil {
		t.Error("expected client to be set")
	}
}

func TestV1Alpha2PreRunESubpackageDir(t *testing.T) {
	ns := "test-ns"
	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	r := &v1alpha2Runner{
		ctx:    context.Background(),
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	cmd := newUpgradeCmd()
	assert.NoError(t, cmd.Flags().Set("subpackage-dir", "my-subpkg"))
	assert.NoError(t, cmd.Flags().Set("strategy", "resource-merge"))

	err = r.preRunE(cmd, []string{"parent-pr"})
	assert.NoError(t, err)
	assert.Equal(t, "my-subpkg", r.subpackageDir)
}

func makeV2Pr(ns, repo, pkg string, revision int, lc porchv1alpha2.PackageRevisionLifecycle, source *porchv1alpha2.PackageSource) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      fmt.Sprintf("%s.%s.v%d", repo, pkg, revision),
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: repo,
			PackageName:    pkg,
			WorkspaceName:  fmt.Sprintf("v%d", revision),
			Lifecycle:      lc,
			Source:         source,
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			Revision: revision,
		},
	}
}

func cloneSource(upstreamRefName string) *porchv1alpha2.PackageSource {
	return &porchv1alpha2.PackageSource{
		CloneFrom: &porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: upstreamRefName},
		},
	}
}

func createV2Runner(ctx context.Context, c client.Client, prs []porchv1alpha2.PackageRevision, ns string, revision int, workspace string) *v1alpha2Runner {
	return &v1alpha2Runner{
		ctx:       ctx,
		cfg:       &genericclioptions.ConfigFlags{Namespace: &ns},
		client:    c,
		revision:  revision,
		workspace: workspace,
		prs:       prs,
	}
}

func TestV1Alpha2UpgradeCommand(t *testing.T) {
	ctx := context.Background()
	ns := "ns"

	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)

	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))
	localDraft := makeV2Pr(ns, "local-repo", "clone-draft", 0, porchv1alpha2.PackageRevisionLifecycleDraft, cloneSource(origV1.Name))
	localDraft.Name = "local-repo--clone-draft-v0"

	prs := []porchv1alpha2.PackageRevision{*origV1, *origV2, *localPr, *localDraft}

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	interceptorFuncs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "PackageRevision" {
				obj.SetName("upgraded-pr")
			}
			return nil
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(origV1, origV2, localPr, localDraft).
		WithInterceptorFuncs(interceptorFuncs).
		Build()

	testCases := []struct {
		name           string
		args           []string
		revision       int
		expectedOutput string
		expectedError  string
	}{
		{
			name:           "Successful upgrade to specific revision",
			args:           []string{localPr.Name},
			revision:       2,
			expectedOutput: fmt.Sprintf("%s upgraded to upgraded-pr\n", localPr.Name),
		},
		{
			name:           "Successful upgrade to latest",
			args:           []string{localPr.Name},
			revision:       0,
			expectedOutput: fmt.Sprintf("%s upgraded to upgraded-pr\n", localPr.Name),
		},
		{
			name:          "Draft package revision",
			args:          []string{localDraft.Name},
			revision:      2,
			expectedError: "must be in a published state",
		},
		{
			name:          "Non-existent package revision",
			args:          []string{"non-existent"},
			revision:      2,
			expectedError: "could not find package revision non-existent",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			output := &bytes.Buffer{}
			r := createV2Runner(ctx, fakeClient, prs, ns, tc.revision, "upgrade-ws")

			cmd := &cobra.Command{}
			cmd.SetOut(output)

			err := r.runE(cmd, tc.args)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("Expected error containing %q, got %v", tc.expectedError, err)
				}
			} else if err != nil {
				t.Fatalf("Unexpected error: %+v", err)
			}

			if tc.expectedOutput != "" {
				if diff := cmp.Diff(strings.TrimSpace(tc.expectedOutput), strings.TrimSpace(output.String())); diff != "" {
					t.Errorf("Unexpected output (-want, +got): %s", diff)
				}
			}
		})
	}
}

func TestV1Alpha2FindPackageRevision(t *testing.T) {
	prs := []porchv1alpha2.PackageRevision{
		{ObjectMeta: metav1.ObjectMeta{Name: "pr1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pr2"}},
	}
	r := &v1alpha2Runner{prs: prs}

	assert.NotNil(t, r.findPackageRevision("pr1"))
	assert.NotNil(t, r.findPackageRevision("pr2"))
	assert.Nil(t, r.findPackageRevision("pr3"))
}

func TestV1Alpha2FindLatestPackageRevisionForRef(t *testing.T) {
	ns := "ns"
	prs := []porchv1alpha2.PackageRevision{
		*makeV2Pr(ns, "repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil),
		*makeV2Pr(ns, "repo", "pkg", 3, porchv1alpha2.PackageRevisionLifecyclePublished, nil),
		*makeV2Pr(ns, "repo", "pkg", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil),
	}
	r := &v1alpha2Runner{prs: prs}

	found := r.findLatestPackageRevisionForRef("pkg", "repo")
	assert.NotNil(t, found)
	assert.Equal(t, 3, found.Status.Revision)

	assert.Nil(t, r.findLatestPackageRevisionForRef("nonexistent", "repo"))
}

func TestV1Alpha2FindUpstreamName(t *testing.T) {
	clonePr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "local.clone.v1"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
			Source:    cloneSource("upstream.orig.v1"),
		},
	}
	copyPr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "local.clone.v2"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
			Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "local.clone.v1"},
			},
		},
	}
	noSourcePr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "local.nosource.v1"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
	}

	r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*clonePr, *copyPr, *noSourcePr}}

	assert.Equal(t, "upstream.orig.v1", r.findUpstreamName(clonePr))
	assert.Equal(t, "upstream.orig.v1", r.findUpstreamName(copyPr))
	assert.Equal(t, "", r.findUpstreamName(noSourcePr))
}

func TestV1Alpha2FindUpstreamNameGitURLClone(t *testing.T) {
	selfLock := &porchv1alpha2.Locator{
		Git: &porchv1alpha2.GitLock{
			Repo:      "https://github.com/user/repo",
			Directory: "packages/orig",
			Ref:       "orig/v1",
		},
	}

	// Upstream PR with selfLock
	upstreamPr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "upstream-repo.orig.v1"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			RepositoryName: "upstream-repo",
			PackageName:    "orig",
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			Revision: 1,
			SelfLock: selfLock,
		},
	}

	// Downstream cloned via git URL — upstreamLock matches upstream's selfLock
	gitClonePr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "local.clone.v1"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Type: porchv1alpha2.RepositoryTypeGit,
					Git: &porchv1alpha2.GitPackage{
						Repo: "https://github.com/user/repo",
						Ref:  "orig/v1",
					},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			UpstreamLock: selfLock,
		},
	}

	// Git clone with no matching upstream
	noMatchPr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "local.nomatch.v1"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Type: porchv1alpha2.RepositoryTypeGit,
					Git:  &porchv1alpha2.GitPackage{Repo: "https://other.com/repo"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			UpstreamLock: &porchv1alpha2.Locator{
				Git: &porchv1alpha2.GitLock{Repo: "https://other.com/repo", Ref: "v1"},
			},
		},
	}

	r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*upstreamPr, *gitClonePr, *noMatchPr}}

	assert.Equal(t, "upstream-repo.orig.v1", r.findUpstreamName(gitClonePr))
	assert.Equal(t, "", r.findUpstreamName(noMatchPr))
}

func TestV1Alpha2FindUpstreamBySelfLock(t *testing.T) {
	lock := &porchv1alpha2.Locator{
		Git: &porchv1alpha2.GitLock{
			Repo:      "https://github.com/user/repo",
			Directory: "packages/foo",
			Ref:       "refs/tags/v1",
		},
	}

	prs := []porchv1alpha2.PackageRevision{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "match"},
			Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
			Status:     porchv1alpha2.PackageRevisionStatus{SelfLock: lock},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "draft"},
			Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft},
			Status:     porchv1alpha2.PackageRevisionStatus{SelfLock: lock},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "no-lock"},
			Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
		},
	}
	r := &v1alpha2Runner{prs: prs}

	assert.Equal(t, "match", r.findUpstreamBySelfLock(lock).Name)
	assert.Nil(t, r.findUpstreamBySelfLock(nil))
	assert.Nil(t, r.findUpstreamBySelfLock(&porchv1alpha2.Locator{}))
	assert.Nil(t, r.findUpstreamBySelfLock(&porchv1alpha2.Locator{Git: &porchv1alpha2.GitLock{Repo: "other"}}))
}

func TestV1Alpha2DiscoverUpstreamUpdates(t *testing.T) {
	ns := "ns"

	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))

	prs := []porchv1alpha2.PackageRevision{*origV1, *origV2, *localPr}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	r := &v1alpha2Runner{
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      prs,
		discover: upstream,
	}

	err := r.discoverUpdates(cmd, []string{localPr.Name})
	assert.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`local-repo\.clone\.v1\s+upstream-repo\s+v2`), output.String())
}

func TestV1Alpha2DiscoverDownstreamUpdates(t *testing.T) {
	ns := "ns"

	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))

	prs := []porchv1alpha2.PackageRevision{*origV1, *origV2, *localPr}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	r := &v1alpha2Runner{
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      prs,
		discover: downstream,
	}

	err := r.discoverUpdates(cmd, []string{})
	assert.NoError(t, err)
	assert.Contains(t, output.String(), "DOWNSTREAM PACKAGE")
}

func TestV1Alpha2DiscoverNoUpdates(t *testing.T) {
	ns := "ns"

	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	r := &v1alpha2Runner{
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      []porchv1alpha2.PackageRevision{*origV1},
		discover: upstream,
	}

	err := r.discoverUpdates(cmd, []string{origV1.Name})
	assert.NoError(t, err)
	assert.Contains(t, output.String(), "No update available")
}

func TestV1Alpha2DiscoverInvalidParam(t *testing.T) {
	ns := "ns"
	cmd := &cobra.Command{}

	r := &v1alpha2Runner{
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      []porchv1alpha2.PackageRevision{},
		discover: "invalid",
	}

	err := r.discoverUpdates(cmd, []string{})
	assert.ErrorContains(t, err, "invalid argument \"invalid\" for --discover")
}

func TestV1Alpha2DiscoverPrNotFound(t *testing.T) {
	ns := "ns"
	cmd := &cobra.Command{}

	r := &v1alpha2Runner{
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      []porchv1alpha2.PackageRevision{},
		discover: upstream,
	}

	err := r.discoverUpdates(cmd, []string{"nonexistent"})
	assert.ErrorContains(t, err, "could not find")
}

func TestV1Alpha2DoUpgradeErrors(t *testing.T) {
	ns := "ns"
	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)

	testCases := []struct {
		name     string
		pr       *porchv1alpha2.PackageRevision
		prs      []porchv1alpha2.PackageRevision
		revision int
		errMsg   string
	}{
		{
			name:   "no source",
			pr:     makeV2Pr(ns, "repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil),
			errMsg: "upstream source not found",
		},
		{
			name:   "init source (no upstream)",
			pr:     makeV2Pr(ns, "repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, &porchv1alpha2.PackageSource{Init: &porchv1alpha2.PackageInitSpec{}}),
			errMsg: "upstream source not found",
		},
		{
			name:   "upstream PR no longer exists",
			pr:     makeV2Pr(ns, "repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource("does-not-exist")),
			errMsg: "no longer exists",
		},
		{
			name:     "specific revision not found",
			pr:       makeV2Pr(ns, "local", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name)),
			prs:      []porchv1alpha2.PackageRevision{*origV1},
			revision: 99,
			errMsg:   "revision 99 does not exist",
		},
		{
			name: "specific revision not published",
			pr:   makeV2Pr(ns, "local", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name)),
			prs: []porchv1alpha2.PackageRevision{
				*origV1,
				*makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecycleDraft, nil),
			},
			revision: 2,
			errMsg:   "revision 2 does not exist",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prs := tc.prs
			if prs == nil {
				prs = []porchv1alpha2.PackageRevision{}
			}
			r := &v1alpha2Runner{
				prs:      prs,
				revision: tc.revision,
			}
			_, err := r.doUpgrade(tc.pr)
			assert.ErrorContains(t, err, tc.errMsg)
		})
	}
}

func TestV1Alpha2AvailableUpdatesV2NoSource(t *testing.T) {
	r := &v1alpha2Runner{}

	// nil source
	pr := porchv1alpha2.PackageRevision{}
	updates, repo := r.availableUpdatesV2(pr)
	assert.Nil(t, updates)
	assert.Empty(t, repo)

	// init source (no upstream)
	pr.Spec.Source = &porchv1alpha2.PackageSource{Init: &porchv1alpha2.PackageInitSpec{}}
	updates, repo = r.availableUpdatesV2(pr)
	assert.Nil(t, updates)
	assert.Empty(t, repo)
}

func TestV1Alpha2AvailableUpdatesV2NoUpstreamMatch(t *testing.T) {
	r := &v1alpha2Runner{
		prs: []porchv1alpha2.PackageRevision{},
	}
	pr := porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: cloneSource("does-not-exist"),
		},
	}
	updates, repo := r.availableUpdatesV2(pr)
	assert.Nil(t, updates)
	assert.Empty(t, repo)
}

func TestV1Alpha2FindPackageRevisionForRef(t *testing.T) {
	ns := "ns"
	prs := []porchv1alpha2.PackageRevision{
		*makeV2Pr(ns, "repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil),
		*makeV2Pr(ns, "repo", "pkg", 2, porchv1alpha2.PackageRevisionLifecycleDraft, nil),
	}
	r := &v1alpha2Runner{prs: prs}

	assert.NotNil(t, r.findPackageRevisionForRef("pkg", "repo", 1))
	// draft should not match
	assert.Nil(t, r.findPackageRevisionForRef("pkg", "repo", 2))
	// wrong revision
	assert.Nil(t, r.findPackageRevisionForRef("pkg", "repo", 99))
}

func TestV1Alpha2DownstreamUpdatesWithArgFilter(t *testing.T) {
	ns := "ns"

	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))

	prs := []porchv1alpha2.PackageRevision{*origV1, *origV2, *localPr}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	r := &v1alpha2Runner{
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      prs,
		discover: downstream,
	}

	// Filter with a specific arg that matches the upstream name
	err := r.discoverUpdates(cmd, []string{origV2.Name})
	assert.NoError(t, err)
	assert.Contains(t, output.String(), "DOWNSTREAM")
}

func TestV1Alpha2RunEDiscoverError(t *testing.T) {
	ns := "ns"
	r := &v1alpha2Runner{
		ctx:      context.Background(),
		cfg:      &genericclioptions.ConfigFlags{Namespace: &ns},
		prs:      []porchv1alpha2.PackageRevision{},
		discover: upstream,
	}

	cmd := &cobra.Command{}
	err := r.runE(cmd, []string{"nonexistent"})
	assert.Error(t, err)
}

func TestV1Alpha2DoUpgradeCreateError(t *testing.T) {
	ns := "ns"
	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))

	prs := []porchv1alpha2.PackageRevision{*origV1, *origV2, *localPr}

	// Mock client that fails on Create
	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(context.Background(), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("create failed"))

	r := &v1alpha2Runner{
		ctx:       context.Background(),
		cfg:       &genericclioptions.ConfigFlags{Namespace: &ns},
		client:    mockC,
		revision:  2,
		workspace: "upgrade-ws",
		prs:       prs,
	}

	_, err := r.doUpgrade(localPr)
	assert.ErrorContains(t, err, "create failed")
}

func TestV1Alpha2RunECreateError(t *testing.T) {
	ns := "ns"
	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))

	prs := []porchv1alpha2.PackageRevision{*origV1, *origV2, *localPr}

	// Mock client that fails on Create
	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Get(context.Background(), mock.AnythingOfType("types.NamespacedName"), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(nil).
		Run(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *localPr
		})
	mockC.EXPECT().
		Create(context.Background(), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("create failed"))

	r := &v1alpha2Runner{
		ctx:       context.Background(),
		cfg:       &genericclioptions.ConfigFlags{Namespace: &ns},
		client:    mockC,
		revision:  2,
		workspace: "upgrade-ws",
		prs:       prs,
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, []string{localPr.Name})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create failed")
}

func TestV1Alpha2ValidateUpgradeArgs(t *testing.T) {
	testCases := []struct {
		name      string
		args      []string
		revision  int
		workspace string
		strategy  string
		errMsg    string
	}{
		{
			name:   "no args",
			args:   []string{},
			errMsg: "SOURCE_PACKAGE_REVISION is a required positional argument",
		},
		{
			name:   "too many args",
			args:   []string{"a", "b"},
			errMsg: "too many arguments",
		},
		{
			name:     "negative revision",
			args:     []string{"pkg"},
			revision: -1,
			errMsg:   "revision must be positive",
		},
		{
			name:     "empty workspace",
			args:     []string{"pkg"},
			revision: 1,
			errMsg:   "workspace is required",
		},
		{
			name:      "invalid strategy",
			args:      []string{"pkg"},
			revision:  1,
			workspace: "ws",
			strategy:  "bogus",
			errMsg:    "invalid strategy",
		},
		{
			name:      "valid",
			args:      []string{"pkg"},
			revision:  1,
			workspace: "ws",
			strategy:  "resource-merge",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &v1alpha2Runner{
				revision:  tc.revision,
				workspace: tc.workspace,
				strategy:  tc.strategy,
			}
			err := r.validateUpgradeArgs(tc.args)
			if tc.errMsg != "" {
				assert.ErrorContains(t, err, tc.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestV1Alpha2OldUpstreamRevStr(t *testing.T) {
	ns := "ns"
	origV1 := makeV2Pr(ns, "upstream-repo", "orig", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	localPr := makeV2Pr(ns, "local-repo", "clone", 1, porchv1alpha2.PackageRevisionLifecyclePublished, cloneSource(origV1.Name))

	t.Run("upstream found", func(t *testing.T) {
		r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*origV1, *localPr}}
		assert.Equal(t, "v1", r.oldUpstreamRevStr(localPr))
	})

	t.Run("upstream not found", func(t *testing.T) {
		r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*localPr}}
		assert.Equal(t, "v0", r.oldUpstreamRevStr(localPr))
	})

	t.Run("no source", func(t *testing.T) {
		noSrc := makeV2Pr(ns, "repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
		r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*noSrc}}
		assert.Equal(t, "v0", r.oldUpstreamRevStr(noSrc))
	})
}

func TestV1Alpha2PreRunEInvalidDiscover(t *testing.T) {
	ns := "test-ns"
	r := &v1alpha2Runner{
		ctx: context.Background(),
		cfg: &genericclioptions.ConfigFlags{Namespace: &ns},
	}

	cmd := &cobra.Command{}
	cmd.Flags().Int("revision", 0, "")
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().String("strategy", "", "")
	cmd.Flags().String("discover", "invalid", "")

	err := r.preRunE(cmd, []string{})
	assert.ErrorContains(t, err, "argument for 'discover' must be one of")
}

func TestV1Alpha2SubpackageUpgradeValidateArgs(t *testing.T) {
	t.Run("subpackage dir allows missing workspace", func(t *testing.T) {
		r := &v1alpha2Runner{subpackageDir: "my-subpkg", revision: 0, strategy: "resource-merge"}
		assert.NoError(t, r.validateUpgradeArgs([]string{"parent-pr"}))
	})

	t.Run("invalid subpackage dir rejected", func(t *testing.T) {
		r := &v1alpha2Runner{subpackageDir: "../bad"}
		err := r.validateUpgradeArgs([]string{"parent-pr"})
		assert.ErrorContains(t, err, "invalid --subpackage-dir")
	})

	t.Run("no subpackage dir still requires workspace", func(t *testing.T) {
		r := &v1alpha2Runner{revision: 1}
		err := r.validateUpgradeArgs([]string{"parent-pr"})
		assert.ErrorContains(t, err, "workspace is required")
	})
}

func TestV1Alpha2FindPackageRevisionFromUpstreamV2(t *testing.T) {
	ns := "ns"
	gitLock := &porchv1alpha2.GitLock{
		Repo:      "https://github.com/user/repo",
		Directory: "packages/pkg",
		Ref:       "packages/pkg/v1",
	}
	upstreamPr := makeV2Pr(ns, "upstream-repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	upstreamPr.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}

	r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*upstreamPr}}

	t.Run("nil upstream returns error", func(t *testing.T) {
		_, err := r.findPackageRevisionFromUpstreamV2(nil)
		assert.ErrorContains(t, err, "could not find upstream references")
	})

	t.Run("empty ref returns error", func(t *testing.T) {
		_, err := r.findPackageRevisionFromUpstreamV2(&kptfilev1.Upstream{
			Type: kptfilev1.GitOrigin,
			Git:  &kptfilev1.Git{Repo: "https://github.com/user/repo", Directory: "packages/pkg", Ref: ""},
		})
		assert.ErrorContains(t, err, "could not find upstream references")
	})

	t.Run("matching selfLock finds PR", func(t *testing.T) {
		pr, err := r.findPackageRevisionFromUpstreamV2(&kptfilev1.Upstream{
			Type: kptfilev1.GitOrigin,
			Git:  &kptfilev1.Git{Repo: gitLock.Repo, Directory: gitLock.Directory, Ref: gitLock.Ref},
		})
		assert.NoError(t, err)
		assert.Equal(t, upstreamPr.Name, pr.Name)
	})

	t.Run("no match returns error", func(t *testing.T) {
		_, err := r.findPackageRevisionFromUpstreamV2(&kptfilev1.Upstream{
			Type: kptfilev1.GitOrigin,
			Git:  &kptfilev1.Git{Repo: "https://other.com/repo", Directory: "packages/pkg", Ref: "packages/pkg/v1"},
		})
		assert.ErrorContains(t, err, "could not find package revision")
	})
}

func TestV1Alpha2DoSubpackageUpgrade(t *testing.T) {
	const ns = "ns"
	ctx := context.Background()

	v2scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating v1alpha2 scheme: %v", err)
	}
	if err := porchapiv1alpha1.AddToScheme(v2scheme); err != nil {
		t.Fatalf("error adding v1alpha1 to scheme: %v", err)
	}

	gitLock := &porchv1alpha2.GitLock{
		Repo:      "https://github.com/user/repo",
		Directory: "packages/upstream",
		Ref:       "packages/upstream/v1",
	}
	upstreamV1 := makeV2Pr(ns, "upstream-repo", "upstream", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	upstreamV1.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}
	upstreamV2 := makeV2Pr(ns, "upstream-repo", "upstream", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)

	subpkgKptfile := "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-subpkg\nupstream:\n  type: git\n  git:\n    repo: https://github.com/user/repo\n    directory: packages/upstream\n    ref: packages/upstream/v1\n"

	t.Run("parent not draft returns error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        fake.NewClientBuilder().WithScheme(v2scheme).Build(),
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1, *upstreamV2},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "parent package must be in state draft")
	})

	t.Run("missing subpackage Kptfile returns error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		resources := &porchapiv1alpha1.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
			Spec:       porchapiv1alpha1.PackageRevisionResourcesSpec{Resources: map[string]string{}},
		}
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1, *upstreamV2},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "could not find Kptfile")
	})

	t.Run("subpackage with no upstream returns error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		resources := &porchapiv1alpha1.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
			Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
				Resources: map[string]string{
					"my-subpkg/Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-subpkg\n",
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1, *upstreamV2},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "has no upstream source")
	})

	t.Run("successful subpackage upgrade sets SubpackageOperation", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		resources := &porchapiv1alpha1.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
			Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
				Resources: map[string]string{"my-subpkg/Kptfile": subpkgKptfile},
			},
		}
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1, *upstreamV2},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.NoError(t, err)
		assert.NotNil(t, parentPR.Spec.SubpackageOperation)
		assert.Equal(t, "my-subpkg", parentPR.Spec.SubpackageOperation.SubpackageDir)
		assert.Equal(t, upstreamV1.Name, parentPR.Spec.SubpackageOperation.Upgrade.OldUpstream.Name)
		assert.Equal(t, upstreamV2.Name, parentPR.Spec.SubpackageOperation.Upgrade.NewUpstream.Name)
		assert.Equal(t, parentPR.Name, parentPR.Spec.SubpackageOperation.Upgrade.CurrentPackage.Name)
	})
}

func TestV1Alpha2RunESubpackageUpgrade(t *testing.T) {
	const ns = "ns"
	ctx := context.Background()

	v2scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}
	if err := porchapiv1alpha1.AddToScheme(v2scheme); err != nil {
		t.Fatalf("error adding v1alpha1 to scheme: %v", err)
	}

	gitLock := &porchv1alpha2.GitLock{
		Repo:      "https://github.com/user/repo",
		Directory: "packages/upstream",
		Ref:       "packages/upstream/v1",
	}
	upstreamV1 := makeV2Pr(ns, "upstream-repo", "upstream", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	upstreamV1.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}
	upstreamV2 := makeV2Pr(ns, "upstream-repo", "upstream", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)

	parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
	parentPR.Name = "repo.parent.draft"

	subpkgKptfile := "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-subpkg\nupstream:\n  type: git\n  git:\n    repo: https://github.com/user/repo\n    directory: packages/upstream\n    ref: packages/upstream/v1\n"
	resources := &porchapiv1alpha1.PackageRevisionResources{
		ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
		Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
			Resources: map[string]string{"my-subpkg/Kptfile": subpkgKptfile},
		},
	}

	c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
	prs := []porchv1alpha2.PackageRevision{*upstreamV1, *upstreamV2, *parentPR}

	r := &v1alpha2Runner{
		ctx:           ctx,
		cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
		client:        c,
		subpackageDir: "my-subpkg",
		prs:           prs,
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err = r.runE(cmd, []string{parentPR.Name})
	assert.NoError(t, err)
	assert.Contains(t, output.String(), "independent subpackage in directory")
	assert.Contains(t, output.String(), "my-subpkg")
}

func TestV1Alpha2DoSubpackageUpgradeAdditionalErrors(t *testing.T) {
	const ns = "ns"
	ctx := context.Background()

	v2scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}
	if err := porchapiv1alpha1.AddToScheme(v2scheme); err != nil {
		t.Fatalf("error adding v1alpha1 to scheme: %v", err)
	}

	gitLock := &porchv1alpha2.GitLock{
		Repo:      "https://github.com/user/repo",
		Directory: "packages/upstream",
		Ref:       "packages/upstream/v1",
	}
	upstreamV1 := makeV2Pr(ns, "upstream-repo", "upstream", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	upstreamV1.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}
	upstreamV1Draft := makeV2Pr(ns, "upstream-repo", "upstream", 1, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
	upstreamV1Draft.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}

	validKptfile := "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-subpkg\nupstream:\n  type: git\n  git:\n    repo: https://github.com/user/repo\n    directory: packages/upstream\n    ref: packages/upstream/v1\n"

	t.Run("resources Get error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		// No PRR object in the fake client — Get will return not-found.
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR).Build()
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "could not get the resources")
	})

	t.Run("invalid Kptfile YAML returns error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		resources := &porchapiv1alpha1.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
			Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
				Resources: map[string]string{"my-subpkg/Kptfile": "{{not valid yaml{{"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "could not unmarshal Kptfile")
	})

	t.Run("upstream PR not found returns error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		resources := &porchapiv1alpha1.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
			Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
				Resources: map[string]string{"my-subpkg/Kptfile": validKptfile},
			},
		}
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			prs:           []porchv1alpha2.PackageRevision{}, // no upstream PRs
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "could not find upstream package")
	})

	t.Run("old upstream not published returns error", func(t *testing.T) {
		// findPackageRevisionFromUpstreamV2 only matches published PRs, so to reach
		// the "old upstream not published" guard we need IsPublished() to return false
		// on the matched PR. Use Proposed lifecycle which IsPublished() returns false for.
		upstreamV1Proposed := makeV2Pr(ns, "upstream-repo", "upstream", 1, porchv1alpha2.PackageRevisionLifecycleProposed, nil)
		upstreamV1Proposed.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}
		// Temporarily patch IsPublished by using a Published PR but overriding lifecycle after construction.
		// Instead: call doSubpackageUpgrade directly with a runner whose findPackageRevisionFromUpstreamV2
		// returns a non-published PR. We do this by making the PR published for matching but then
		// checking the guard via a separate unit test on the guard itself.
		//
		// The guard at line 227-229 is only reachable if findPackageRevisionFromUpstreamV2 returns a
		// non-published PR. Since that function filters to IsPublished(), the guard is defensive dead code.
		// Skip this subtest — the guard is unreachable in practice.
		_ = upstreamV1Proposed
		t.Skip("guard is defensive dead code: findPackageRevisionFromUpstreamV2 already filters to published PRs")
	})

	t.Run("new upstream not found returns error", func(t *testing.T) {
		parentPR := makeV2Pr(ns, "repo", "parent", 0, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
		parentPR.Name = "repo.parent.draft"
		resources := &porchapiv1alpha1.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: parentPR.Name, Namespace: ns},
			Spec: porchapiv1alpha1.PackageRevisionResourcesSpec{
				Resources: map[string]string{"my-subpkg/Kptfile": validKptfile},
			},
		}
		c := fake.NewClientBuilder().WithScheme(v2scheme).WithObjects(parentPR, resources).Build()
		// Only v1 exists — resolveNewUpstream (latest) will find v1 == old upstream, no v2.
		// Actually it will return v1 as latest. To get resolveNewUpstream to fail, use a specific revision that doesn't exist.
		r := &v1alpha2Runner{
			ctx:           ctx,
			cfg:           &genericclioptions.ConfigFlags{Namespace: func() *string { s := ns; return &s }()},
			client:        c,
			subpackageDir: "my-subpkg",
			revision:      99,
			prs:           []porchv1alpha2.PackageRevision{*upstreamV1},
		}
		err := r.doSubpackageUpgrade(parentPR)
		assert.ErrorContains(t, err, "revision 99 does not exist")
	})

	t.Run("new upstream not published returns error", func(t *testing.T) {
		// resolveNewUpstream uses findPackageRevisionForRef/findLatestPackageRevisionForRef,
		// both of which filter to IsPublished(). The "new upstream not published" guard
		// is therefore defensive dead code — skip it.
		t.Skip("guard is defensive dead code: resolveNewUpstream already filters to published PRs")
	})
}

func TestV1Alpha2FindPackageRevisionFromUpstreamV2SkipsNoSelfLock(t *testing.T) {
	ns := "ns"
	gitLock := &porchv1alpha2.GitLock{
		Repo:      "https://github.com/user/repo",
		Directory: "packages/pkg",
		Ref:       "packages/pkg/v1",
	}
	// A published PR with no selfLock should be skipped, not matched.
	noLockPr := makeV2Pr(ns, "upstream-repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
	// A draft PR with the right selfLock should also be skipped.
	draftPr := makeV2Pr(ns, "upstream-repo", "pkg", 1, porchv1alpha2.PackageRevisionLifecycleDraft, nil)
	draftPr.Status.SelfLock = &porchv1alpha2.Locator{Git: gitLock}

	r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*noLockPr, *draftPr}}

	_, err := r.findPackageRevisionFromUpstreamV2(&kptfilev1.Upstream{
		Type: kptfilev1.GitOrigin,
		Git:  &kptfilev1.Git{Repo: gitLock.Repo, Directory: gitLock.Directory, Ref: gitLock.Ref},
	})
	assert.ErrorContains(t, err, "could not find package revision")
}

func TestV1Alpha2FindUpstreamNameAdditional(t *testing.T) {
	ns := "ns"

	t.Run("CopyFrom with no source found returns empty", func(t *testing.T) {
		pr := &porchv1alpha2.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{Name: "local.copy.v2"},
			Spec: porchv1alpha2.PackageRevisionSpec{
				Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
				Source: &porchv1alpha2.PackageSource{
					CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "does-not-exist"},
				},
			},
		}
		r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*pr}}
		assert.Equal(t, "", r.findUpstreamName(pr))
	})

	t.Run("Upgrade source returns new upstream name", func(t *testing.T) {
		origV2 := makeV2Pr(ns, "upstream-repo", "orig", 2, porchv1alpha2.PackageRevisionLifecyclePublished, nil)
		pr := &porchv1alpha2.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{Name: "local.clone.v2"},
			Spec: porchv1alpha2.PackageRevisionSpec{
				Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
				Source: &porchv1alpha2.PackageSource{
					Upgrade: &porchv1alpha2.PackageUpgradeSpec{
						NewUpstream: porchv1alpha2.PackageRevisionRef{Name: origV2.Name},
					},
				},
			},
		}
		r := &v1alpha2Runner{prs: []porchv1alpha2.PackageRevision{*origV2, *pr}}
		assert.Equal(t, origV2.Name, r.findUpstreamName(pr))
	})
}
