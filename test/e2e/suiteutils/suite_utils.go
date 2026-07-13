// Copyright 2024-2026 The kpt Authors
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

package suiteutils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	kptfilesdk "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	coreapi "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	defaultKrmFuncRegistry = "ghcr.io/kptdev/krm-functions-catalog"
	sleepImage             = "sleep:latest"
)

var (
	PackageRevisionGVK = porchapi.SchemeGroupVersion.WithKind("PackageRevision")
)

type MetricsCollectionResults struct {
	PorchServerMetrics         string
	PorchControllerMetrics     string
	PorchFunctionRunnerMetrics string
	PorchWrapperServerMetrics  string
}

type ParsedMetricsResults struct {
	PorchServerMetrics         map[string][]MetricResult
	PorchControllerMetrics     map[string][]MetricResult
	PorchFunctionRunnerMetrics map[string][]MetricResult
	PorchWrapperServerMetrics  map[string][]MetricResult
}

type MetricResult struct {
	Value      model.SampleValue
	Attributes model.LabelSet
}

func (r *MetricsCollectionResults) Parse() (*ParsedMetricsResults, error) {
	parsed := &ParsedMetricsResults{
		PorchServerMetrics:         make(map[string][]MetricResult),
		PorchControllerMetrics:     make(map[string][]MetricResult),
		PorchFunctionRunnerMetrics: make(map[string][]MetricResult),
		PorchWrapperServerMetrics:  make(map[string][]MetricResult),
	}
	metricsParser := expfmt.NewTextParser(model.LegacyValidation)

	for _, pair := range []struct {
		raw          string
		parsedResult map[string][]MetricResult
		nameForError string
	}{
		{r.PorchServerMetrics, parsed.PorchServerMetrics, "PorchServerMetrics"},
		{r.PorchControllerMetrics, parsed.PorchControllerMetrics, "PorchControllerMetrics"},
		{r.PorchFunctionRunnerMetrics, parsed.PorchFunctionRunnerMetrics, "PorchFunctionRunnerMetrics"},
		{r.PorchWrapperServerMetrics, parsed.PorchWrapperServerMetrics, "PorchWrapperServerMetrics"},
	} {
		if pair.raw == "" {
			continue
		}

		metricFamilies, err := metricsParser.TextToMetricFamilies(strings.NewReader(pair.raw))
		if err != nil {
			return nil, fmt.Errorf("error extracting metrics from %s text: %w", pair.nameForError, err)
		}

		for metricName, family := range metricFamilies {
			samples, err := expfmt.ExtractSamples(&expfmt.DecodeOptions{}, family)
			if err != nil {
				return nil, fmt.Errorf("error extracting %s metric sample for %q: %w", pair.nameForError, metricName, err)
			}
			for _, sample := range samples {
				pair.parsedResult[metricName] = append(pair.parsedResult[metricName], MetricResult{
					Value:      sample.Value,
					Attributes: model.LabelSet(sample.Metric),
				})
			}
		}
	}

	return parsed, nil
}

type TestSuiteWithGit struct {
	TestSuite
	gitConfig            GitConfig
	UseGitea             bool
	KrmFunctionsRegistry string
}

func (t *TestSuiteWithGit) SetupSuite() {
	t.KrmFunctionsRegistry = defaultKrmFuncRegistry
	t.SetupEnvVars()
	t.TestSuite.SetupSuite()
	if !t.UseGitea {
		// This is using the legacy stubbed git server
		// which is no longer supported. Use the test gitea repo instead.
		t.gitConfig = t.CreateGitRepo()
	}
}

func (t *TestSuiteWithGit) SetupEnvVars() {
	if customRegistry := os.Getenv("KRM_FN_REGISTRY_URL"); customRegistry != "" {
		t.KrmFunctionsRegistry = customRegistry
	}
}

func (t *TestSuite) ValidateFinalizers(name string, finalizers []string) {
	t.T().Helper()
	var pr porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      name,
	}, &pr)

	if len(finalizers) != len(pr.Finalizers) {
		diff := cmp.Diff(finalizers, pr.Finalizers)
		t.Errorf("Expected %d finalizers, but got %s", len(finalizers), diff)
	}

	for _, finalizer := range finalizers {
		var found bool
		for _, f := range pr.Finalizers {
			if f == finalizer {
				found = true
			}
		}
		if !found {
			t.Errorf("Expected finalizer %v, but didn't find it", finalizer)
		}
	}
}

func (t *TestSuite) ValidateOwnerReferences(name string, ownerRefs []metav1.OwnerReference) {
	t.T().Helper()
	var pr porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      name,
	}, &pr)

	if len(ownerRefs) != len(pr.OwnerReferences) {
		diff := cmp.Diff(ownerRefs, pr.OwnerReferences)
		t.Errorf("Expected %d ownerReferences, but got %s", len(ownerRefs), diff)
	}

	for _, ownerRef := range ownerRefs {
		var found bool
		for _, or := range pr.OwnerReferences {
			if or == ownerRef {
				found = true
			}
		}
		if !found {
			t.Errorf("Expected ownerRef %v, but didn't find it", ownerRef)
		}
	}
}

func (t *TestSuite) ValidateLabelsAndAnnos(name string, labels, annos map[string]string) {
	t.T().Helper()
	var pr porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      name,
	}, &pr)

	actualLabels := pr.Labels
	actualAnnos := pr.Annotations

	// Make this check to handle empty vs nil maps
	if len(labels) != 0 || len(actualLabels) != 0 {
		if diff := cmp.Diff(actualLabels, labels); diff != "" {
			t.Errorf("Unexpected result (-want, +got): %s", diff)
		}
	}

	if len(annos) != 0 || len(actualAnnos) != 0 {
		if diff := cmp.Diff(actualAnnos, annos); diff != "" {
			t.Errorf("Unexpected result (-want, +got): %s", diff)
		}
	}
}

func (t *TestSuite) MustHaveLabels(name string, labels map[string]string) {
	t.T().Helper()
	var pr porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      name,
	}, &pr)

	for labelKey, labelValue := range labels {
		actualValue, ok := pr.Labels[labelKey]
		if !ok {
			t.Errorf("Expected PR %s to have label %s, but didn't find it", pr.Name, labelKey)
		}
		if actualValue != labelValue {
			t.Errorf("Expected PR %s to have label %s value %s but got %s", pr.Name, labelKey, labelValue, actualValue)
		}
	}
}

func (t *TestSuite) MustNotHaveLabels(name string, labels []string) {
	t.T().Helper()
	var pr porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      name,
	}, &pr)

	for _, label := range labels {
		_, ok := pr.Labels[label]
		if ok {
			t.Errorf("Expected PR %s not to have label %s, but found it", pr.Name, label)
		}
	}
}

func (t *TestSuite) RegisterGitRepositoryF(repo, name, directory string, username string, password Password, opts ...RepositoryOptions) {
	t.T().Helper()
	config := GitConfig{
		Repo:      repo,
		Branch:    "main",
		Directory: directory,
		Username:  username,
		Password:  password,
	}
	t.registerGitRepositoryFromConfigF(name, config, opts...)
}

func (t *TestSuite) registerGitRepositoryFromConfigF(name string, config GitConfig, opts ...RepositoryOptions) {
	t.T().Helper()

	repo := &configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       configapi.TypeRepository.Kind,
			APIVersion: configapi.GroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: t.Namespace,
		},
		Spec: configapi.RepositorySpec{
			Description: "Porch Test Repository Description",
			Type:        configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:      config.Repo,
				Branch:    config.Branch,
				Directory: config.Directory,
				SecretRef: configapi.SecretRef{
					Name: t.CreateOrUpdateSecret(name, config.Username, config.Password, opts...),
				},
			},
		},
	}

	// Apply options
	for _, opt := range opts {
		if opt.RepOpts != nil {
			opt.RepOpts(repo)
		}
	}

	// Register repository
	t.CreateF(repo)

	t.Cleanup(func() {
		t.DeleteE(repo)
		t.WaitUntilRepositoryDeleted(name, repo.Namespace)
		t.WaitUntilAllPackagesDeleted(name, repo.Namespace)
		if IsPorchTestRepo(config.Repo) {
			t.RecreateGiteaTestRepo()
		}
	})

	// Make sure the repository is ready before we test to (hopefully)
	// avoid flakiness.
	t.WaitUntilRepositoryReady(repo.Name, repo.Namespace)
	t.Logf("Repository %s/%s is ready", repo.Namespace, repo.Name)
}

func (t *TestSuite) CreateOrUpdateSecret(name string, username string, password Password, opts ...RepositoryOptions) string {
	t.T().Helper()

	if username == "" && password == "" {
		return ""
	}

	secretName := fmt.Sprintf("%s-auth", name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: t.Namespace,
		},
		Immutable: ptr.To(true),
		Data: map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		},
		Type: corev1.SecretTypeBasicAuth,
	}
	for _, opt := range opts {
		if opt.SecOpts != nil {
			opt.SecOpts(secret)
		}
	}
	t.CreateOrUpdateF(secret)
	t.Cleanup(func() {
		t.T().Helper()
		t.DeleteE(secret)
	})
	return secretName
}

type RepositoryOptions struct {
	RepOpts RepositoryOption
	SecOpts SecretOption
}

type RepositoryOption func(*configapi.Repository)

type SecretOption func(*corev1.Secret)

func WithBranch(branch string) RepositoryOption {
	return func(r *configapi.Repository) {
		r.Spec.Git.Branch = branch
	}
}

func WithDeployment() RepositoryOption {
	return func(r *configapi.Repository) {
		r.Spec.Deployment = true
	}
}

func WithType(t configapi.RepositoryType) RepositoryOption {
	return func(r *configapi.Repository) {
		r.Spec.Type = t
	}
}

func InNamespace(ns string) RepositoryOption {
	return func(repo *configapi.Repository) {
		repo.Namespace = ns
	}
}

func SecretInNamespace(ns string) SecretOption {
	return func(secret *corev1.Secret) {
		secret.Namespace = ns
	}
}

// Creates an empty package draft by initializing an empty package
func (t *TestSuite) CreatePackageDraftF(repository, packageName, workspace string) *porchapi.PackageRevision {
	t.T().Helper()
	pr := t.CreatePackageSkeleton(repository, packageName, workspace)
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeInit,
			Init: &porchapi.PackageInitTaskSpec{
				Description: packageName + " description",
			},
		},
	}
	t.CreateF(pr)
	return pr
}

// CreatePackageCloneF creates a package revision with a clone task.
// Assumes the GitePackage.SecretRef was created by t.RegisterGitRepositoryF.
func (t *TestSuite) CreatePackageCloneF(repoName, packageName, workspace, ref, directory string) *porchapi.PackageRevision {
	t.T().Helper()
	pr := t.CreatePackageSkeleton(repoName, packageName, workspace)
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					Type: porchapi.RepositoryTypeGit,
					Git: &porchapi.GitPackage{
						Repo:      t.GetTestBlueprintsRepoURL(),
						Ref:       ref,
						Directory: directory,
						SecretRef: porchapi.SecretRef{
							Name: fmt.Sprintf("%s-auth", repoName),
						},
					},
				},
			},
		},
	}
	t.CreateF(pr)
	return pr
}

func (t *TestSuite) CreatePackageSkeleton(repoName, packageName, workspace string) *porchapi.PackageRevision {
	return &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       PackageRevisionGVK.Kind,
			APIVersion: porchapi.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    packageName,
			WorkspaceName:  workspace,
			RepositoryName: repoName,
			// empty tasks list - set them as needed in the particular usage
			Tasks: []porchapi.Task{},
		},
	}
}

func (t *TestSuite) MustExist(key client.ObjectKey, obj client.Object) {
	t.T().Helper()
	t.Logf("Checking existence of %q...", key)
	t.GetF(key, obj)
	if got, want := obj.GetName(), key.Name; got != want {
		t.Errorf("%T.Name: got %q, want %q", obj, got, want)
	}
	if got, want := obj.GetNamespace(), key.Namespace; got != want {
		t.Errorf("%T.Namespace: got %q, want %q", obj, got, want)
	}
}

func (t *TestSuite) MustNotExist(obj client.Object) {
	t.T().Helper()
	switch err := t.Reader.Get(t.GetContext(), client.ObjectKeyFromObject(obj), obj); {
	case err == nil:
		t.Errorf("No error returned getting a deleted package; expected error")
	case !apierrors.IsNotFound(err):
		t.Errorf("Expected NotFound error. got %v", err)
	}
}

// WaitUntilRepositoryReady waits for up to 300 seconds for the repository with the
// provided name and namespace is ready, i.e. the Ready condition is true.
// It also queries for PackageRevisions, to ensure these are also
// ready - this is an artifact of the way we've implemented the aggregated apiserver,
// where the first fetch can sometimes be synchronous.
func (t *TestSuite) WaitUntilRepositoryReady(name, namespace string) {
	t.T().Helper()
	nn := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	var innerErr error
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 300*time.Second, true, func(ctx context.Context) (bool, error) {
		var repo configapi.Repository
		if err := t.Reader.Get(ctx, nn, &repo); err != nil {
			innerErr = err
			return false, nil
		}
		for _, c := range repo.Status.Conditions {
			if c.Type == configapi.RepositoryReady {
				if c.Status == metav1.ConditionTrue {
					return true, nil
				} else {
					innerErr = fmt.Errorf("error condition is false: %s", c.Message)
					return false, nil
				}
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("Repository not ready after wait: %v", innerErr)
	}

	// While we're using an aggregated apiserver, make sure we can query the generated objects
	if err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 32*time.Second, true, func(ctx context.Context) (bool, error) {
		var revisions porchapi.PackageRevisionList
		if err := t.Reader.List(ctx, &revisions, client.InNamespace(nn.Namespace)); err != nil {
			innerErr = err
			return false, nil
		}
		return true, nil
	}); err != nil {
		t.Fatalf("unable to query PackageRevisions after wait: %v", innerErr)
	}
}

func (t *TestSuite) WaitUntilMultipleRepositoriesReady(waitingRepos []configapi.Repository) {
	t.T().Helper()

	repoNames := func() (names []string) {
		for _, repo := range waitingRepos {
			names = append(names, repo.Name)
		}
		return
	}()

	t.Logf("Waiting for %d repositories in namespace %q to be ready: %s", len(repoNames), t.Namespace, repoNames)

	var innerErr error
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 300*time.Second, true, func(ctx context.Context) (bool, error) {
		var repos configapi.RepositoryList
		if err := t.Reader.List(t.GetContext(), &repos, client.InNamespace(t.Namespace)); err != nil {
			innerErr = err
			return false, err
		}

		for _, each := range repoNames {
			if !slices.ContainsFunc(repos.Items, func(aRepo configapi.Repository) bool { return aRepo.Name == each }) {
				return false, nil
			}
		}

		allReady := !slices.ContainsFunc(repos.Items, func(aRepo configapi.Repository) bool {
			return slices.Contains(repoNames, aRepo.Name) &&
				(aRepo.Status.Conditions == nil ||
					slices.ContainsFunc(aRepo.Status.Conditions, func(aCondition metav1.Condition) bool {
						return aCondition.Type == configapi.RepositoryReady && aCondition.Status != metav1.ConditionTrue
					}))
		})
		return allReady, nil
	})
	if err != nil {
		t.Fatalf("Repositories not ready after wait: %w (inner error: %w)", err, innerErr)
	}
}

func (t *TestSuite) WaitUntilAllPackageVariantsReady() {
	t.T().Helper()

	var innerErr error
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 300*time.Second, true, func(ctx context.Context) (bool, error) {
		var repos configapi.PackageVariantList
		if err := t.Reader.List(t.GetContext(), &repos, client.InNamespace(t.Namespace)); err != nil {
			innerErr = err
			return false, err
		}

		allReady := !slices.ContainsFunc(repos.Items, func(aRepo configapi.PackageVariant) bool {
			return aRepo.Status.Conditions == nil || slices.ContainsFunc(aRepo.Status.Conditions, func(aCondition metav1.Condition) bool {
				return aCondition.Type == configapi.RepositoryReady && aCondition.Status != metav1.ConditionTrue
			})
		})
		return allReady, nil
	})
	if err != nil {
		t.Fatalf("Repositories not ready after wait: %v", innerErr)
	}
}

func (t *TestSuite) WaitUntilRepositoryDeleted(name, namespace string) {
	t.T().Helper()
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 20*time.Second, true, func(ctx context.Context) (done bool, err error) {
		var repo configapi.Repository
		nn := types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}
		if err := t.Reader.Get(ctx, nn, &repo); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("Repository %s/%s not deleted", namespace, name)
	}
}

func (t *TestSuite) WaitUntilAllPackageRevisionsDeleted(repoName string, namespace string) {
	t.T().Helper()
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 60*time.Second, true, func(ctx context.Context) (done bool, err error) {
		var pkgRevList porchapi.PackageRevisionList
		if err := t.Reader.List(ctx, &pkgRevList, client.InNamespace(namespace)); err != nil {
			t.Logf("error listing PackageRevisions: %v", err)
			return false, nil
		}
		for _, pkgRev := range pkgRevList.Items {
			if pkgRev.Spec.RepositoryName == repoName {
				t.Logf("Found PackageRevision %s from repo %s", pkgRev.Name, repoName)
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("PackageRevisions from repo %s still remain", repoName)
	}
}

func (t *TestSuite) WaitUntilAllPackageRevsDeleted(repoName string, namespace string) {
	t.T().Helper()
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 60*time.Second, true, func(ctx context.Context) (done bool, err error) {
		var internalPkgRevList configapi.PackageRevList
		if err := t.Reader.List(ctx, &internalPkgRevList, client.InNamespace(namespace), client.MatchingLabels{
			"internal.porch.kpt.dev/repository": repoName,
		}); err != nil {
			t.Logf("error listing PackageRevs: %v", err)
			return false, nil
		}
		if len(internalPkgRevList.Items) > 0 {
			t.Logf("Found %d PackageRevs from repo %s", len(internalPkgRevList.Items), repoName)
			for _, internalPkgRev := range internalPkgRevList.Items {
				if len(internalPkgRev.Finalizers) > 0 {
					t.removePkgRevFinalizers(ctx, &internalPkgRev)
				}
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("PackageRevs from repo %s still remain", repoName)
	}
}

func (t *TestSuite) WaitUntilAllPackagesDeleted(repoName string, namespace string) {
	t.T().Helper()
	t.WaitUntilAllPackageRevisionsDeleted(repoName, namespace)
	t.WaitUntilAllPackageRevsDeleted(repoName, namespace)
}

func (t *TestSuite) WaitUntilObjectDeleted(gvk schema.GroupVersionKind, namespacedName types.NamespacedName, d time.Duration) {
	t.T().Helper()
	var innerErr error
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, d, true, func(ctx context.Context) (bool, error) {
		var u unstructured.Unstructured
		u.SetGroupVersionKind(gvk)
		if err := t.Reader.Get(ctx, namespacedName, &u); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			innerErr = err
			return false, err
		}
		return false, nil
	})
	if err != nil {
		t.Errorf("Object %s not deleted after %s: %v", namespacedName.String(), d.String(), innerErr)
	}
}

func (t *TestSuite) WaitUntilPackageRevisionFulfillingConditionExists(
	timeout time.Duration,
	condition func(porchapi.PackageRevision) bool,
) (*porchapi.PackageRevision, error) {

	t.T().Helper()
	var foundPkgRev *porchapi.PackageRevision
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		var pkgRevList porchapi.PackageRevisionList
		if err := t.Reader.List(ctx, &pkgRevList); err != nil {
			t.Logf("error listing packages: %v", err)
			return false, nil
		}
		for _, pkgRev := range pkgRevList.Items {
			if condition(pkgRev) {
				foundPkgRev = &pkgRev
				return true, nil
			}
		}
		return false, nil
	})
	return foundPkgRev, err
}

func (t *TestSuite) WaitUntilPackageRevisionExists(repository string, pkgName string, revision int) *porchapi.PackageRevision {
	t.T().Helper()
	t.Logf("Waiting for package revision (%v/%v/%v) to exist", repository, pkgName, revision)
	timeout := 120 * time.Second
	foundPkgRev, err := t.WaitUntilPackageRevisionFulfillingConditionExists(timeout, func(pkgRev porchapi.PackageRevision) bool {
		return pkgRev.Spec.RepositoryName == repository &&
			pkgRev.Spec.PackageName == pkgName &&
			pkgRev.Spec.Revision == revision
	})
	if err != nil {
		t.Fatalf("Package revision (%v/%v/%v) not found in time (%v)", repository, pkgName, revision, timeout)
	}
	return foundPkgRev
}

func (t *TestSuite) WaitUntilPackageRevisionResourcesExists(
	key types.NamespacedName,
) *porchapi.PackageRevisionResources {

	t.T().Helper()
	t.Logf("Waiting for PackageRevisionResources object %v to exist", key)
	timeout := 120 * time.Second
	var foundPrr *porchapi.PackageRevisionResources
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		var prrList porchapi.PackageRevisionResourcesList
		if err := t.Reader.List(ctx, &prrList); err != nil {
			t.Logf("error listing package revision resources: %v", err)
			return false, nil
		}
		for _, prr := range prrList.Items {
			if client.ObjectKeyFromObject(&prr) == key {
				foundPrr = &prr
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("PackageRevisionResources object wasn't found for package %v in time (%v)", key, timeout)
	}
	return foundPrr
}

func (t *TestSuite) GetPackageRevision(repo string, pkgName string, revision int) *porchapi.PackageRevision {
	t.T().Helper()
	var prList porchapi.PackageRevisionList
	selector := client.MatchingFields(fields.Set{
		"spec.repository":  repo,
		"spec.packageName": pkgName,
		"spec.revision":    strconv.Itoa(revision),
	})
	t.ListF(&prList, selector, client.InNamespace(t.Namespace))

	if len(prList.Items) == 0 {
		t.Fatalf("PackageRevision object wasn't found for package revision %v/%v/%d", repo, pkgName, revision)
	}
	if len(prList.Items) > 1 {
		t.Fatalf("Multiple PackageRevision objects were found for package revision %v/%v/%d", repo, pkgName, revision)
	}
	return &prList.Items[0]
}

func (t *TestSuite) RetriggerBackgroundJobForRepo(repoName string) {
	repoKey := client.ObjectKey{
		Namespace: t.Namespace,
		Name:      repoName,
	}
	var repo configapi.Repository
	t.GetF(repoKey, &repo)
	repo.ResourceVersion = ""

	// Delete and recreate repository to trigger background job on it
	t.DeleteE(&repo)
	t.WaitUntilRepositoryDeleted(repo.Name, t.Namespace)
	time.Sleep(2 * time.Second)
	t.CreateE(&repo)
	t.WaitUntilRepositoryReady(repo.Name, t.Namespace)
}

type MutatorOption func(*kptfilev1.Function)

func WithConfigmap(configMap map[string]string) MutatorOption {
	return func(r *kptfilev1.Function) {
		r.ConfigMap = configMap
	}
}

func WithConfigPath(configPath string) MutatorOption {
	return func(r *kptfilev1.Function) {
		r.ConfigPath = configPath
	}
}

func SplitContainerFullName(fullName string) (repo, image, tag string) {
	slash := strings.LastIndex(fullName, "/")
	repo = fullName[:slash+1]
	imageAndTag := fullName[slash+1:]
	colon := strings.LastIndex(imageAndTag, ":")
	if colon >= 0 {
		return repo, imageAndTag[:colon], imageAndTag[colon:]
	} else {
		return repo, imageAndTag, ""
	}
}

func (t *TestSuite) FindFirstContainerByImageName(ns string, wantedImages ...string) *coreapi.Container {
	var pods coreapi.PodList
	if err := t.Client.List(t.GetContext(), &pods, client.InNamespace(ns)); err != nil {
		t.Logf("FindFirstContainerByImageName: Failed to list pods in namespace %s: %v", ns, err)
		return nil
	}

	t.ListF(&pods, client.InNamespace(ns))
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			_, imageGot, _ := SplitContainerFullName(container.Image)
			for _, imageWanted := range wantedImages {
				if strings.Contains(imageGot, imageWanted) {
					return container.DeepCopy()
				}
			}
		}
	}
	return nil
}

func (t *TestSuite) FnNamespaceName() string {
	defaultNamespace := "porch-fn-system"

	porchSvcKey := t.PorchServerServiceKey()

	container := t.FindFirstContainerByImageName(porchSvcKey.Namespace, "porch-function-runner", "porch-fnrunner")
	if container != nil {
		for _, arg := range container.Args {
			if strings.Contains(arg, "pod-namespace") {
				elements := strings.Split(arg, "=")
				return elements[len(elements)-1]
			}
		}
		for _, command := range container.Command {
			if strings.Contains(command, "pod-namespace") {
				elements := strings.Split(command, "=")
				return elements[len(elements)-1]
			}
		}

	}

	return defaultNamespace
}

func (t *TestSuiteWithGit) AddSleepFunctionToPipeline(prKey client.ObjectKey, sleepDuration time.Duration) error {
	t.T().Helper()
	resources := &porchapi.PackageRevisionResources{}
	err := t.Client.Get(t.GetContext(), prKey, resources)
	if err != nil {
		return err
	}
	kptfile, err := kptfilesdk.NewFromPackage(resources.Spec.Resources)
	if err != nil {
		return err
	}

	err = kptfile.UpsertMutatorFunctions([]kptfilev1.Function{
		{
			Name:  "test-sleep",
			Image: t.KrmFunctionsRegistry + "/" + sleepImage,
			ConfigMap: map[string]string{
				"duration": sleepDuration.String(),
			},
		},
	}, -1)
	if err != nil {
		return err
	}

	err = kptfile.WriteToPackage(resources.Spec.Resources)
	if err != nil {
		return err
	}

	err = t.Client.Update(t.GetContext(), resources)
	if err != nil {
		return err
	}

	err = t.CheckRenderError(&resources.Status.RenderStatus)
	if err != nil {
		return err
	}

	return nil
}

func (t *TestSuite) CheckRenderError(rs *porchapi.RenderStatus) error {
	if rs.Err != "" {
		return fmt.Errorf("failed to render package: %s", rs.Err)
	}

	if rs.Result.ExitCode != 0 {
		var errorDetails strings.Builder
		errorDetails.WriteString(fmt.Sprintf("render pipeline failed with overall exit code %d.", rs.Result.ExitCode))

		for _, item := range rs.Result.Items {
			if item != nil && item.ExitCode != 0 {
				errorDetails.WriteString(fmt.Sprintf("\n  - Function %q failed with exit code %d. Stderr: %s", item.Image, item.ExitCode, item.Stderr))
			}
		}
		return errors.New(errorDetails.String())
	}

	return nil
}

// addMutator adds a mutator to the Kptfile pipeline of the resources (in-place)
func (t *TestSuite) AddMutator(resources *porchapi.PackageRevisionResources, image string, opts ...MutatorOption) {
	t.T().Helper()
	kptf, ok := resources.Spec.Resources[kptfilev1.KptFileName]
	if !ok {
		t.Fatalf("Kptfile not found in resources")
	}
	parsed := &kptfilev1.KptFile{}
	if err := yaml.Unmarshal([]byte(kptf), parsed); err != nil {
		t.Fatalf("Failed to unmarshal Kptfile: %v", err)
	}

	if parsed.Pipeline == nil {
		parsed.Pipeline = &kptfilev1.Pipeline{}
	}

	if parsed.Pipeline.Mutators == nil {
		parsed.Pipeline.Mutators = make([]kptfilev1.Function, 0, 1)
	}

	parsed.Pipeline.Mutators = append(parsed.Pipeline.Mutators, kptfilev1.Function{Image: image})
	mut := &parsed.Pipeline.Mutators[len(parsed.Pipeline.Mutators)-1]

	for _, opt := range opts {
		opt(mut)
	}

	marshalled, err := yaml.Marshal(parsed)
	if err != nil {
		t.Fatalf("Failed to marshal Kptfile: %v", err)
	}

	resources.Spec.Resources[kptfilev1.KptFileName] = string(marshalled)
}

func (t *TestSuite) AddResourceToPackage(resources *porchapi.PackageRevisionResources, filePath string, name string) {
	t.T().Helper()
	file, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read file from %q: %v", filePath, err)
	}
	resources.Spec.Resources[name] = string(file)
}

func (t *TestSuite) CollectMetricsFromPods() (*MetricsCollectionResults, error) {
	ctx := context.Background()
	podList, err := t.KubeClient.CoreV1().Pods("porch-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list pods: %v", err)
		return nil, err
	}
	if len(podList.Items) == 0 {
		t.Fatalf("no pods found")
	}
	var porchServerPod *corev1.Pod
	var porchControllersPod *corev1.Pod
	var porchFunctionRunnerPod *corev1.Pod

	for _, pod := range podList.Items {
		if strings.HasPrefix(pod.Name, "porch-server") {
			porchServerPod = &pod
		}
		if strings.HasPrefix(pod.Name, "porch-controllers") {
			porchControllersPod = &pod
		}
		if strings.HasPrefix(pod.Name, "function-runner") {
			porchFunctionRunnerPod = &pod
		}
	}

	collectionResults := &MetricsCollectionResults{}

	functionPodList, err := t.KubeClient.CoreV1().Pods("porch-fn-system").List(ctx, metav1.ListOptions{})
	t.Require().NoError(err, "failed to list pods from porch-fn-system")
	t.Require().Greater(len(functionPodList.Items), 0, "expected at least one pod in porch-fn-system")

	functionPod := functionPodList.Items[0]

	if porchServerPod == nil || porchControllersPod == nil || porchFunctionRunnerPod == nil {
		t.Fatalf("failed to find pods")
	}

	resp, err := t.KubeClient.CoreV1().Pods("porch-system").ProxyGet("", porchServerPod.Name, "9464", "metrics", nil).DoRaw(ctx)
	t.Require().NoError(err, "failed to get metrics for porch-server")
	collectionResults.PorchServerMetrics = string(resp)

	resp, err = t.KubeClient.CoreV1().Pods("porch-system").ProxyGet("", porchControllersPod.Name, "9464", "metrics", nil).DoRaw(ctx)
	if err != nil {
		t.Require().NoError(err, "failed to get metrics for porch-controllers")
	}
	collectionResults.PorchControllerMetrics = string(resp)

	resp, err = t.KubeClient.CoreV1().Pods("porch-system").ProxyGet("", porchFunctionRunnerPod.Name, "9464", "metrics", nil).DoRaw(ctx)
	if err != nil {
		t.Require().NoError(err, "failed to get metrics for function-runner")
	}
	collectionResults.PorchFunctionRunnerMetrics = string(resp)

	resp, err = t.KubeClient.CoreV1().Pods("porch-fn-system").ProxyGet("", functionPod.Name, "9464", "metrics", nil).DoRaw(ctx)
	if err != nil {
		t.Require().NoError(err, "failed to get metrics for wrapper-server")
	}
	collectionResults.PorchWrapperServerMetrics = string(resp)

	return collectionResults, nil
}

func (t *TestSuite) TimingHelper(operationDescription string, toTime func(t *TestSuite)) {
	t.T().Helper()
	start := time.Now()

	defer func() {
		t.T().Helper()
		descForLog := func() string {
			if operationDescription != "" {
				return " to " + operationDescription
			}
			return ""
		}()
		t.Logf("took %v%s", time.Since(start), descForLog)
	}()

	toTime(t)
}

func RunInParallel(functions ...func() any) []any {
	var group sync.WaitGroup
	var mu sync.Mutex
	results := make([]any, len(functions))

	startSignal := make(chan struct{})

	for i, eachFunction := range functions {
		group.Add(1)
		go func(index int, fn func() any) {
			defer group.Done()

			<-startSignal

			var result any
			if reflect.TypeOf(fn).NumOut() == 0 {
				fn()
				result = nil
			} else {
				result = fn()
			}

			mu.Lock()
			results[index] = result
			mu.Unlock()
		}(i, eachFunction)
	}
	close(startSignal)

	group.Wait()
	return results
}

func (t *TestSuite) removePkgRevFinalizers(ctx context.Context, pkgRev *configapi.PackageRev) {
	t.Logf("removing finalizers from orphaned PackageRev %s/%s", pkgRev.Namespace, pkgRev.Name)
	pkgRev.Finalizers = []string{}
	for range 3 {
		if err := t.Client.Update(ctx, pkgRev); err != nil {
			if apierrors.IsConflict(err) {
				key := client.ObjectKeyFromObject(pkgRev)
				if getErr := t.Client.Get(ctx, key, pkgRev); getErr != nil {
					if apierrors.IsNotFound(getErr) {
						return
					}
					continue
				}
				pkgRev.Finalizers = []string{}
				continue
			}
			t.Logf("failed to remove finalizers from PackageRev %s/%s: %v", pkgRev.Namespace, pkgRev.Name, err)
		}
		return
	}
}
