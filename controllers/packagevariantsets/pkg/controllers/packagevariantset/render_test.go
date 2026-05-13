// Copyright 2023, 2025 The kpt Authors
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

package packagevariantset

import (
	"context"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	pkgvarapi "github.com/kptdev/porch/controllers/packagevariants/api/v1alpha1"
	api "github.com/kptdev/porch/controllers/packagevariantsets/api/v1alpha2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ptr "k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

var repoListYaml = []byte(`
apiVersion: config.porch.kpt.dev/v1alpha1
kind: RepositoryList
metadata:
  name: my-repo-list
items:
- apiVersion: config.porch.kpt.dev/v1alpha1
  kind: Repository
  metadata:
    name: my-repo-1
    labels:
      foo: bar
      abc: def
- apiVersion: config.porch.kpt.dev/v1alpha1
  kind: Repository
  metadata:
    name: my-repo-2
    labels:
      foo: bar
      abc: def
      efg: hij
`)

func TestRenderPackageVariantSpec(t *testing.T) {
	var repoList configapi.RepositoryList
	require.NoError(t, yaml.Unmarshal(repoListYaml, &repoList))

	adoptExisting := pkgvarapi.AdoptionPolicyAdoptExisting
	deletionPolicyDelete := pkgvarapi.DeletionPolicyDelete
	pvs := api.PackageVariantSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pvs",
			Namespace: "default",
		},
		Spec: api.PackageVariantSetSpec{
			Upstream: &pkgvarapi.Upstream{Repo: "up-repo", Package: "up-pkg", Revision: 2},
		},
	}
	upstreamPR := porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "default",
			Labels: map[string]string{
				"vendor":  "bigco.com",
				"product": "snazzy",
				"version": "1.6.8",
			},
			Annotations: map[string]string{
				"bigco.com/team": "us-platform",
			},
		},
	}
	testCases := map[string]struct {
		downstream   pvContext
		expectedSpec pkgvarapi.PackageVariantSpec
		expectedErrs []string
	}{
		"no template": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
			},
			expectedErrs: nil,
		},
		"template downstream.repo": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Downstream: &api.DownstreamTemplate{
						Repo: ptr.To("my-repo-2"),
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-2",
					Package: "p",
				},
			},
			expectedErrs: nil,
		},
		"template downstream.package": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Downstream: &api.DownstreamTemplate{
						Package: ptr.To("new-p"),
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "new-p",
				},
			},
			expectedErrs: nil,
		},
		"template adoption and deletion": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					AdoptionPolicy: &adoptExisting,
					DeletionPolicy: &deletionPolicyDelete,
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
				AdoptionPolicy: "adoptExisting",
				DeletionPolicy: "delete",
			},
			expectedErrs: nil,
		},
		"template static labels and annotations": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Labels: map[string]string{
						"foo":   "bar",
						"hello": "there",
					},
					Annotations: map[string]string{
						"foobar": "barfoo",
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
				Labels: map[string]string{
					"foo":   "bar",
					"hello": "there",
				},
				Annotations: map[string]string{
					"foobar": "barfoo",
				},
			},
			expectedErrs: nil,
		},
		"template static packageContext": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					PackageContext: &api.PackageContextTemplate{
						Data: map[string]string{
							"foo":   "bar",
							"hello": "there",
						},
						RemoveKeys: []string{"foobar", "barfoo"},
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
				PackageContext: &pkgvarapi.PackageContext{
					Data: map[string]string{
						"foo":   "bar",
						"hello": "there",
					},
					RemoveKeys: []string{"barfoo", "foobar"},
				},
			},
			expectedErrs: nil,
		},
		"template downstream with expressions": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Downstream: &api.DownstreamTemplate{
						RepoExpr:    ptr.To("'my-repo-2'"),
						PackageExpr: ptr.To("repoDefault + '-' + packageDefault"),
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-2",
					Package: "my-repo-1-p",
				},
			},
			expectedErrs: nil,
		},
		"template labels and annotations with expressions": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Downstream: &api.DownstreamTemplate{
						RepoExpr:    ptr.To("'my-repo-2'"),
						PackageExpr: ptr.To("repoDefault + '-' + packageDefault"),
					},
					Labels: map[string]string{
						"foo":   "bar",
						"hello": "there",
					},
					LabelExprs: []api.MapExpr{
						{
							Key:       ptr.To("foo"),
							ValueExpr: ptr.To("repoDefault"),
						},
						{
							KeyExpr:   ptr.To("repository.labels['efg']"),
							ValueExpr: ptr.To("packageDefault + '-' + repository.name"),
						},
						{
							Key:   ptr.To("hello"),
							Value: ptr.To("goodbye"),
						},
					},
					Annotations: map[string]string{
						"bigco.com/sample-annotation": "some-annotation",
						"foo.org/id":                  "123456",
					},
					AnnotationExprs: []api.MapExpr{
						{
							Key:   ptr.To("foo.org/id"),
							Value: ptr.To("54321"),
						},
						{
							Key:       ptr.To("bigco.com/team"),
							ValueExpr: ptr.To("upstream.annotations['bigco.com/team']"),
						},
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-2",
					Package: "my-repo-1-p",
				},
				Labels: map[string]string{
					"foo":   "my-repo-1",
					"hello": "goodbye",
					"hij":   "p-my-repo-2",
				},
				Annotations: map[string]string{
					"bigco.com/sample-annotation": "some-annotation",
					"foo.org/id":                  "54321",
					"bigco.com/team":              "us-platform",
				},
			},
			expectedErrs: nil,
		},
		"template with packageContext with expressions": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					PackageContext: &api.PackageContextTemplate{
						Data: map[string]string{
							"foo":   "bar",
							"hello": "there",
						},
						DataExprs: []api.MapExpr{
							{
								Key:       ptr.To("foo"),
								ValueExpr: ptr.To("upstream.name"),
							},
							{
								KeyExpr:   ptr.To("upstream.namespace"),
								ValueExpr: ptr.To("upstream.name"),
							},
							{
								KeyExpr: ptr.To("upstream.name"),
								Value:   ptr.To("foo"),
							},
						},
						RemoveKeys:     []string{"foobar", "barfoo"},
						RemoveKeyExprs: []string{"repository.labels['abc']"},
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
				PackageContext: &pkgvarapi.PackageContext{
					Data: map[string]string{
						"foo":     "p",
						"hello":   "there",
						"default": "p",
						"p":       "foo",
					},
					RemoveKeys: []string{"barfoo", "def", "foobar"},
				},
			},
			expectedErrs: nil,
		},
		"template injectors": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Injectors: []api.InjectionSelectorTemplate{
						{
							Group:   ptr.To("kpt.dev"),
							Version: ptr.To("v1alpha1"),
							Kind:    ptr.To("Foo"),
							Name:    ptr.To("bar"),
						},
						{
							Group:    ptr.To("kpt.dev"),
							Version:  ptr.To("v1alpha1"),
							Kind:     ptr.To("Foo"),
							NameExpr: ptr.To("repository.labels['abc']"),
						},
						{
							NameExpr: ptr.To("repository.name + '-test'"),
						},
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
				Injectors: []pkgvarapi.InjectionSelector{
					{
						Group:   ptr.To("kpt.dev"),
						Version: ptr.To("v1alpha1"),
						Kind:    ptr.To("Foo"),
						Name:    "bar",
					},
					{
						Group:   ptr.To("kpt.dev"),
						Version: ptr.To("v1alpha1"),
						Kind:    ptr.To("Foo"),
						Name:    "def",
					},
					{
						Name: "my-repo-1-test",
					},
				},
			},
			expectedErrs: nil,
		},
		"pipeline injectors": {
			downstream: pvContext{
				repoDefault:    "my-repo-1",
				packageDefault: "p",
				template: &api.PackageVariantTemplate{
					Pipeline: &api.PipelineTemplate{
						Validators: []api.FunctionTemplate{
							{
								Function: kptfilev1.Function{
									Image: "foo:bar",
									Name:  "hey",
								},
							},
							{
								Function: kptfilev1.Function{
									Image: "foo:bar",
									ConfigMap: map[string]string{
										"k1": "v1",
										"k2": "v2",
									},
								},
								ConfigMapExprs: []api.MapExpr{
									{
										Key:       ptr.To("k1"),
										ValueExpr: ptr.To("repository.name"),
									},
									{
										KeyExpr: ptr.To("'k3'"),
										Value:   ptr.To("bar"),
									},
								},
							},
						},
						Mutators: []api.FunctionTemplate{
							{
								Function: kptfilev1.Function{
									Image: "mutates",
								},
								ConfigMapExprs: []api.MapExpr{
									{
										Key:   ptr.To("k1"),
										Value: ptr.To("yo"),
									},
								},
							},
						},
					},
				},
			},
			expectedSpec: pkgvarapi.PackageVariantSpec{
				Upstream: pvs.Spec.Upstream,
				Downstream: &pkgvarapi.Downstream{
					Repo:    "my-repo-1",
					Package: "p",
				},
				Pipeline: &kptfilev1.Pipeline{
					Validators: []kptfilev1.Function{
						{
							Image: "foo:bar",
							Name:  "hey",
						},
						{
							Image: "foo:bar",
							ConfigMap: map[string]string{
								"k1": "my-repo-1",
								"k2": "v2",
								"k3": "bar",
							},
						},
					},
					Mutators: []kptfilev1.Function{
						{
							Image: "mutates",
							ConfigMap: map[string]string{
								"k1": "yo",
							},
						},
					},
				},
			},
			expectedErrs: nil,
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			pvSpec, err := renderPackageVariantSpec(context.Background(), &pvs, &repoList, &upstreamPR, tc.downstream)
			require.NoError(t, err)
			require.Equal(t, &tc.expectedSpec, pvSpec)
		})
	}
}

func TestEvalExpr(t *testing.T) {
	baseInputs := map[string]interface{}{
		"repoDefault":    "foo-repo",
		"packageDefault": "bar-package",
	}
	var repoList configapi.RepositoryList
	require.NoError(t, yaml.Unmarshal(repoListYaml, &repoList))

	r1Input, err := objectToInput(&repoList.Items[0])
	require.NoError(t, err)

	testCases := map[string]struct {
		expr           string
		target         interface{}
		expectedResult string
		expectedErr    string
	}{
		"no vars": {
			expr:           "'foo'",
			expectedResult: "foo",
			expectedErr:    "",
		},
		"repoDefault": {
			expr:           "repoDefault",
			expectedResult: "foo-repo",
			expectedErr:    "",
		},
		"packageDefault": {
			expr:           "packageDefault",
			expectedResult: "bar-package",
			expectedErr:    "",
		},
		"concat defaults": {
			expr:           "packageDefault + '-' + repoDefault",
			expectedResult: "bar-package-foo-repo",
			expectedErr:    "",
		},
		"repositories target": {
			expr: "target.repo + '/' + target.package",
			target: map[string]any{
				"repo":    "my-repo",
				"package": "my-package",
			},
			expectedResult: "my-repo/my-package",
			expectedErr:    "",
		},
		"repository target": {
			expr:           "target.name + '/' + target.labels['foo']",
			target:         r1Input,
			expectedResult: "my-repo-1/bar",
			expectedErr:    "",
		},
		"bad variable": {
			expr:        "badvar",
			expectedErr: "ERROR: <input>:1:1: undeclared reference to 'badvar' (in container '')\n | badvar\n | ^",
		},
		"bad expr": {
			expr:        "/",
			expectedErr: "ERROR: <input>:1:1: Syntax error: mismatched input '/' expecting {'[', '{', '(', '.', '-', '!', 'true', 'false', 'null', NUM_FLOAT, NUM_INT, NUM_UINT, STRING, BYTES, IDENTIFIER}\n | /\n | ^\nERROR: <input>:1:2: Syntax error: mismatched input '<EOF>' expecting {'[', '{', '(', '.', '-', '!', 'true', 'false', 'null', NUM_FLOAT, NUM_INT, NUM_UINT, STRING, BYTES, IDENTIFIER}\n | /\n | .^",
		},
		"missing label": {
			expr:        "target.name + '/' + target.labels['no-such-label']",
			target:      r1Input,
			expectedErr: "no such key: no-such-label",
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			inputs := map[string]any{}
			for k, v := range baseInputs {
				inputs[k] = v
			}
			inputs["target"] = tc.target
			val, err := evalExpr(tc.expr, inputs)
			if tc.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, val)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}

func TestCopyAndOverlayMapExpr(t *testing.T) {
	baseInputs := map[string]interface{}{
		"repoDefault":    "foo-repo",
		"packageDefault": "bar-package",
	}

	testCases := map[string]struct {
		inMap          map[string]string
		mapExprs       []api.MapExpr
		expectedResult map[string]string
		expectedErr    string
	}{
		"empty starting map": {
			inMap: map[string]string{},
			mapExprs: []api.MapExpr{
				{
					Key:   ptr.To("foo"),
					Value: ptr.To("bar"),
				},
				{
					KeyExpr: ptr.To("repoDefault"),
					Value:   ptr.To("barbar"),
				},
				{
					Key:       ptr.To("bar"),
					ValueExpr: ptr.To("packageDefault"),
				},
			},
			expectedResult: map[string]string{
				"foo":      "bar",
				"foo-repo": "barbar",
				"bar":      "bar-package",
			},
		},
		"static overlay": {
			inMap: map[string]string{
				"foo": "bar",
				"bar": "foo",
			},
			mapExprs: []api.MapExpr{
				{
					Key:   ptr.To("foo"),
					Value: ptr.To("new-bar"),
				},
				{
					Key:   ptr.To("foofoo"),
					Value: ptr.To("barbar"),
				},
			},
			expectedResult: map[string]string{
				"foo":    "new-bar",
				"bar":    "foo",
				"foofoo": "barbar",
			},
		},
		"exprs overlay": {
			inMap: map[string]string{
				"foo": "bar",
				"bar": "foo",
			},
			mapExprs: []api.MapExpr{
				{
					KeyExpr: ptr.To("'foo'"),
					Value:   ptr.To("new-bar"),
				},
				{
					Key:       ptr.To("bar"),
					ValueExpr: ptr.To("packageDefault"),
				},
			},
			expectedResult: map[string]string{
				"foo": "new-bar",
				"bar": "bar-package",
			},
		},
	}
	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			outMap, err := copyAndOverlayMapExpr("f", tc.inMap, tc.mapExprs, baseInputs)
			if tc.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, outMap)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}
