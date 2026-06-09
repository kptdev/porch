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

package suiteutils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	pvapi "github.com/kptdev/porch/controllers/packagevariants/api/v1alpha1"
	pvsetapi "github.com/kptdev/porch/controllers/packagevariantsets/api/v1alpha2"
	internalapi "github.com/kptdev/porch/internal/api/porchinternal/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	aggregatorv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	// TODO: accept a flag?
	PorchTestConfigFile = "porch-test-config.yaml"
	updateGoldenFiles   = "UPDATE_GOLDEN_FILES"
)

type GitConfig struct {
	Repo      string   `json:"repo"`
	Branch    string   `json:"branch"`
	Directory string   `json:"directory"`
	Username  string   `json:"username"`
	Password  Password `json:"password"`
}

type Password string

func (p Password) String() string {
	return "*************"
}

type TestSuite struct {
	suite.Suite
	ctx context.Context

	Kubeconfig *rest.Config
	Client     client.Client
	Reader     client.Reader

	// Strongly-typed client handy for reading e.g. pod logs
	KubeClient kubernetes.Interface

	Namespace               string // K8s namespace for this test run
	TestRunnerIsLocal       bool   // Tests running against local dev porch
	porchServerInCluster    *bool  // Cached result of IsPorchServerInCluster check
	repoControllerInCluster *bool  // Cached result of IsRepoControllerInCluster check
	UsingDBCache            bool   // Tests running against Porch with database cache
}

func (t *TestSuite) SetupSuite() {
	t.ctx = context.Background()
	t.Initialize()
}

func (t *TestSuite) Initialize() {
	cfg, err := config.GetConfig()
	if err != nil {
		t.Skipf("Skipping test suite - cannot obtain k8s client config: %v", err)
	}

	t.Logf("Testing against server: %q", cfg.Host)
	cfg.UserAgent = "Porch Test"
	t.Logf("using timeout %v", cfg.Timeout)

	scheme := createClientScheme(t.T())

	if c, err := client.New(cfg, client.Options{
		Scheme: scheme,
	}); err != nil {
		t.Fatalf("Failed to initialize k8s client (%s): %v", cfg.Host, err)
	} else {
		t.Client = c
		t.Kubeconfig = cfg
	}

	// Create a direct API reader to avoid stale cache reads
	if r, err := client.New(cfg, client.Options{
		Scheme: scheme,
		Cache: &client.CacheOptions{
			DisableFor: []client.Object{},
		},
	}); err != nil {
		t.Fatalf("Failed to initialize direct API reader (%s): %v", cfg.Host, err)
	} else {
		t.Reader = r
	}

	if kubeClient, err := kubernetes.NewForConfig(cfg); err != nil {
		t.Fatalf("failed to initialize kubernetes clientset: %v", err)
	} else {
		t.KubeClient = kubeClient
	}

	t.TestRunnerIsLocal = !t.IsTestRunnerInCluster()

	namespace := fmt.Sprintf("porch-test-%d", time.Now().UnixMicro())
	t.CreateF(&coreapi.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	})

	t.Namespace = namespace

	t.checkIfUsingDBCache()

	c := t.Client
	t.Cleanup(func() {
		if err := c.Delete(t.GetContext(), &coreapi.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}); err != nil {
			t.Errorf("Failed to clean up namespace %q: %v", namespace, err)
		} else {
			t.Logf("Successfully cleaned up namespace %q", namespace)
		}
	})
}

func (t *TestSuite) checkIfUsingDBCache() {
	_, t.UsingDBCache = os.LookupEnv("DB_CACHE")
}

func (t *TestSuite) PorchServerServiceKey() client.ObjectKey {
	porchApiService := aggregatorv1.APIService{}
	apiServiceName := porchapi.SchemeGroupVersion.Version + "." + porchapi.SchemeGroupVersion.Group
	t.GetF(client.ObjectKey{Name: apiServiceName}, &porchApiService)

	if porchApiService.Spec.Service == nil {
		t.Fatalf("Porch APIService %q found, but its Spec.Service field is nil. Cannot determine Porch server Service.", apiServiceName)
	}

	return client.ObjectKey{
		Namespace: porchApiService.Spec.Service.Namespace,
		Name:      porchApiService.Spec.Service.Name,
	}
}

func (t *TestSuite) IsPorchServerInCluster() bool {
	if t.porchServerInCluster != nil {
		return *t.porchServerInCluster
	}

	porch := aggregatorv1.APIService{}
	t.GetF(client.ObjectKey{
		Name: porchapi.SchemeGroupVersion.Version + "." + porchapi.SchemeGroupVersion.Group,
	}, &porch)
	service := coreapi.Service{}
	t.GetF(client.ObjectKey{
		Namespace: porch.Spec.Service.Namespace,
		Name:      porch.Spec.Service.Name,
	}, &service)

	result := len(service.Spec.Selector) > 0
	t.porchServerInCluster = &result
	return result
}

func (t *TestSuite) IsRepoControllerInCluster() bool {
	if t.repoControllerInCluster != nil {
		return *t.repoControllerInCluster
	}

	deployment := appsv1.Deployment{}
	err := t.Client.Get(t.GetContext(), client.ObjectKey{
		Namespace: "porch-system",
		Name:      "porch-controllers",
	}, &deployment)
	if err != nil {
		result := false
		t.repoControllerInCluster = &result
		return false
	}

	result := deployment.Spec.Replicas != nil && *deployment.Spec.Replicas > 0
	t.repoControllerInCluster = &result
	return result
}

func (t *TestSuite) IsTestRunnerInCluster() bool {
	runLocally := os.Getenv("RUN_E2E_LOCALLY")
	if strings.ToLower(runLocally) == "true" {
		return false
	}

	porch := aggregatorv1.APIService{}
	ctx := context.TODO()
	t.GetF(client.ObjectKey{
		Name: porchapi.SchemeGroupVersion.Version + "." + porchapi.SchemeGroupVersion.Group,
	}, &porch)
	service := coreapi.Service{}
	err := t.Client.Get(ctx, client.ObjectKey{
		Namespace: porch.Spec.Service.Namespace,
		Name:      "function-runner",
	}, &service)
	if err != nil {
		return false
	}
	return len(service.Spec.Selector) > 0
}

func (t *TestSuite) Name() string {
	t.T().Helper()
	return t.T().Name()
}

func (t *TestSuite) GetContext() context.Context {
	t.T().Helper()
	return t.ctx
}

func (t *TestSuite) Skipf(format string, args ...any) {
	t.T().Helper()
	t.T().Skipf(format, args...)
}

func (t *TestSuite) Cleanup(f func()) {
	t.T().Helper()
	t.T().Cleanup(f)
}

func (t *TestSuite) Log(args ...any) {
	t.T().Helper()
	args = append([]any{"INFO:"}, args)
	t.T().Log(args...)
}

func (t *TestSuite) Logf(format string, args ...any) {
	t.T().Helper()
	t.T().Logf("INFO: "+format, args...)
}

func (t *TestSuite) Error(args ...any) {
	t.T().Helper()
	args = append([]any{"ERROR:"}, args...)
	t.T().Error(args...)
}

func (t *TestSuite) Errorf(format string, args ...any) {
	t.T().Helper()
	t.T().Errorf("ERROR: "+format, args...)
}

func (t *TestSuite) Fatal(args ...any) {
	t.T().Helper()
	args = append([]any{"FATAL:"}, args...)
	t.T().Fatal(args...)
}

func (t *TestSuite) Fatalf(format string, args ...any) {
	t.T().Helper()
	t.T().Fatalf("FATAL: "+format, args...)
}

type ErrorHandler func(format string, args ...interface{})

func (t *TestSuite) get(key client.ObjectKey, obj client.Object, eh ErrorHandler) {
	t.T().Helper()
	if err := t.Reader.Get(t.GetContext(), key, obj); err != nil {
		eh("failed to get resource %T %s: %v", obj, key, err)
	}
}

func (t *TestSuite) list(list client.ObjectList, opts []client.ListOption, eh ErrorHandler) {
	t.T().Helper()
	if err := t.Reader.List(t.GetContext(), list, opts...); err != nil {
		eh("failed to list resources %T %+v: %v", list, list, err)
	}
}

func DebugFormat(obj client.Object) string {
	var s string
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		s = fmt.Sprintf("%T", obj)
	} else {
		s = gvk.Kind
	}
	ns := obj.GetNamespace()
	if ns != "" {
		s += " " + ns + "/"
	} else {
		s += " "
	}
	s += obj.GetName()
	return s
}

func (t *TestSuite) create(obj client.Object, opts []client.CreateOption, eh ErrorHandler) {
	t.T().Helper()
	t.Logf("creating object %v", DebugFormat(obj))
	start := time.Now()
	defer func() {
		t.T().Helper()
		t.Logf("took %v to create %s/%s", time.Since(start), obj.GetNamespace(), obj.GetName())
	}()

	if err := t.Client.Create(t.GetContext(), obj, opts...); err != nil {
		eh("failed to create resource %s: %v", DebugFormat(obj), err)
	}
}

func (t *TestSuite) createOrUpdate(obj client.Object, opts []client.CreateOption, eh ErrorHandler) {
	t.T().Helper()
	t.Logf("creating object %v", DebugFormat(obj))
	start := time.Now()
	succeededOp := "create"
	defer func() {
		t.T().Helper()
		t.Logf("took %v to %s %s/%s", time.Since(start), succeededOp, obj.GetNamespace(), obj.GetName())
	}()

	// For create, object MUST NOT have resourceVersion set.
	obj.SetResourceVersion("")
	if err := t.Client.Create(t.GetContext(), obj, opts...); err != nil {
		if apierrors.IsAlreadyExists(err) {
			succeededOp = "update"
			t.Logf("resource already exists - attempting to update resource %v", DebugFormat(obj))

			// For update, object MUST have resourceVersion set - get the
			// 		existing object first to make sure we have the most
			// 		up-to-date resourceVersion.
			upToDateObj := obj.DeepCopyObject().(client.Object)
			t.Reader.Get(t.GetContext(), client.ObjectKeyFromObject(obj), upToDateObj)
			obj.SetResourceVersion(upToDateObj.GetResourceVersion())
			if err := t.Client.Update(t.GetContext(), obj); err != nil {
				eh("failed to update resource %s: %v", DebugFormat(obj), err)
			}
		} else {
			eh("failed to create resource %s: %v", DebugFormat(obj), err)
		}
	}
}

func (t *TestSuite) delete(obj client.Object, opts []client.DeleteOption, eh ErrorHandler) {
	t.T().Helper()
	t.Logf("deleting object %v", DebugFormat(obj))

	if err := client.IgnoreNotFound(t.Client.Delete(t.GetContext(), obj, opts...)); err != nil {
		eh("failed to delete resource %s: %v", DebugFormat(obj), err)
	}
}

func (t *TestSuite) update(obj client.Object, opts []client.UpdateOption, eh ErrorHandler) {
	t.T().Helper()
	t.Logf("updating object %v", DebugFormat(obj))

	if err := t.Client.Update(t.GetContext(), obj, opts...); err != nil {
		eh("failed to update resource %s: %v", DebugFormat(obj), err)
	}
}

func (t *TestSuite) patch(obj client.Object, patch client.Patch, opts []client.PatchOption, eh ErrorHandler) {
	t.T().Helper()
	t.Logf("patching object %v", DebugFormat(obj))

	if err := t.Client.Patch(t.GetContext(), obj, patch, opts...); err != nil {
		eh("failed to patch resource %s: %v", DebugFormat(obj), err)
	}
}

// deleteAllOf(ctx context.Context, obj Object, opts ...DeleteAllOfOption) error

func (t *TestSuite) GetE(key client.ObjectKey, obj client.Object) {
	t.T().Helper()
	t.get(key, obj, t.Errorf)
}

func (t *TestSuite) GetF(key client.ObjectKey, obj client.Object) {
	t.T().Helper()
	t.get(key, obj, t.Fatalf)
}

func (t *TestSuite) ListE(list client.ObjectList, opts ...client.ListOption) {
	t.T().Helper()
	t.list(list, opts, t.Errorf)
}

func (t *TestSuite) ListF(list client.ObjectList, opts ...client.ListOption) {
	t.T().Helper()
	t.list(list, opts, t.Fatalf)
}

func (t *TestSuite) CreateF(obj client.Object, opts ...client.CreateOption) {
	t.T().Helper()
	t.create(obj, opts, t.Fatalf)
}

func (t *TestSuite) CreateE(obj client.Object, opts ...client.CreateOption) {
	t.T().Helper()
	t.create(obj, opts, t.Errorf)
}

func (t *TestSuite) CreateOrUpdateF(obj client.Object, opts ...client.CreateOption) {
	t.T().Helper()
	t.createOrUpdate(obj, opts, t.Fatalf)
}

func (t *TestSuite) CreateOrUpdateE(obj client.Object, opts ...client.CreateOption) {
	t.T().Helper()
	t.createOrUpdate(obj, opts, t.Errorf)
}

func (t *TestSuite) DeleteF(obj client.Object, opts ...client.DeleteOption) {
	t.T().Helper()
	t.delete(obj, opts, t.Fatalf)
}

func (t *TestSuite) DeleteE(obj client.Object, opts ...client.DeleteOption) {
	t.T().Helper()
	t.delete(obj, opts, t.Errorf)
}

func (t *TestSuite) DeleteL(obj client.Object, opts ...client.DeleteOption) {
	t.T().Helper()
	t.delete(obj, opts, t.Logf)
}

// DeleteEH calls delete with a custom ErrorHandler
func (t *TestSuite) DeleteEH(obj client.Object, eh ErrorHandler, opts ...client.DeleteOption) {
	t.T().Helper()
	t.delete(obj, opts, eh)
}

func (t *TestSuite) UpdateF(obj client.Object, opts ...client.UpdateOption) {
	t.T().Helper()
	t.update(obj, opts, t.Fatalf)
}

func (t *TestSuite) UpdateE(obj client.Object, opts ...client.UpdateOption) {
	t.T().Helper()
	t.update(obj, opts, t.Errorf)
}

func (t *TestSuite) PatchF(obj client.Object, patch client.Patch, opts ...client.PatchOption) {
	t.T().Helper()
	t.patch(obj, patch, opts, t.Fatalf)
}

func (t *TestSuite) PatchE(obj client.Object, patch client.Patch, opts ...client.PatchOption) {
	t.T().Helper()
	t.patch(obj, patch, opts, t.Errorf)
}

func (t *TestSuite) UpdateApprovalL(pr *porchapi.PackageRevision) *porchapi.PackageRevision {
	t.T().Helper()
	t.Logf("updating approval of %v", DebugFormat(pr))
	if err := t.Client.SubResource("approval").Update(t.GetContext(), pr); err != nil {
		t.Logf("failed to update approval of %s/%s: %v", pr.Namespace, pr.Name, err)
		return nil
	}
	return pr
}

func (t *TestSuite) UpdateApprovalF(pr *porchapi.PackageRevision) *porchapi.PackageRevision {
	t.T().Helper()
	t.Logf("updating approval of %v", DebugFormat(pr))
	if err := t.Client.SubResource("approval").Update(t.GetContext(), pr); err != nil {
		t.Fatalf("failed to update approval of %s/%s: %v", pr.Namespace, pr.Name, err)
		return nil
	}
	return pr
}

// UpdateApprovalE returns error for concurrent testing
func (t *TestSuite) UpdateApprovalE(pr *porchapi.PackageRevision) error {
	t.T().Helper()
	t.Logf("updating approval of %v", DebugFormat(pr))
	prCopy := pr.DeepCopy()
	return t.Client.SubResource("approval").Update(t.GetContext(), prCopy)
}

func createClientScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()

	for _, api := range (runtime.SchemeBuilder{
		porchapi.AddToScheme,
		pvsetapi.AddToScheme,
		pvapi.AddToScheme,
		internalapi.AddToScheme,
		configapi.AddToScheme,
		coreapi.AddToScheme,
		aggregatorv1.AddToScheme,
		appsv1.AddToScheme,
	}) {
		if err := api(scheme); err != nil {
			t.Fatalf("Failed to initialize test k8s api client")
		}
	}
	return scheme
}

func (t *TestSuite) ParseKptfileF(resources *porchapi.PackageRevisionResources) *kptfilev1.KptFile {
	t.T().Helper()
	contents, ok := resources.Spec.Resources[kptfilev1.KptFileName]
	if !ok {
		t.Fatalf("Kptfile not found in %s/%s package", resources.Namespace, resources.Name)
	}
	kptfile, err := kptfileutil.DecodeKptfile(strings.NewReader(contents))
	if err != nil {
		t.Fatalf("Cannot decode Kptfile (%s): %v", contents, err)
	}
	return kptfile
}

func (t *TestSuite) SaveKptfileF(resources *porchapi.PackageRevisionResources, kptfile *kptfilev1.KptFile) {
	t.T().Helper()
	b, err := yaml.MarshalWithOptions(kptfile, &yaml.EncoderOptions{SeqIndent: yaml.WideSequenceStyle})
	if err != nil {
		t.Fatalf("Failed saving Kptfile: %v", err)
	}
	resources.Spec.Resources[kptfilev1.KptFileName] = string(b)
}

func (t *TestSuite) FindAndDecodeF(resources *porchapi.PackageRevisionResources, name string, value interface{}) {
	t.T().Helper()
	contents, ok := resources.Spec.Resources[name]
	if !ok {
		t.Fatalf("Cannot find %q in %s/%s package", name, resources.Namespace, resources.Name)
	}
	var temp interface{}
	d := yaml.NewDecoder(strings.NewReader(contents))
	if err := d.Decode(&temp); err != nil {
		t.Fatalf("Cannot decode yaml %q in %s/%s package: %v", name, resources.Namespace, resources.Name, err)
	}
	jsonData, err := json.Marshal(temp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if err := json.Unmarshal(jsonData, value); err != nil {
		t.Fatalf("json.Unmarshal failed; %v", err)
	}
}

func (t *TestSuite) CompareStringYAML(expectedContents string, gotContents string) string {
	t.T().Helper()
	expectedContents = t.normalizeYamlOrdering(expectedContents)
	gotContents = t.normalizeYamlOrdering(gotContents)
	return cmp.Diff(expectedContents, gotContents)
}

func (t *TestSuite) normalizeYamlOrdering(contents string) string {
	t.T().Helper()
	var data any
	if err := yaml.Unmarshal([]byte(contents), &data); err != nil {
		// not yaml.
		t.Fatalf("Failed to unmarshal yaml: %v\n%s\n", err, contents)
	}

	var stable bytes.Buffer
	encoder := yaml.NewEncoder(&stable)
	encoder.SetIndent(2)
	if err := encoder.Encode(data); err != nil {
		t.Fatalf("Failed to re-encode yaml output: %v", err)
	}
	return stable.String()
}

func (t *TestSuite) MustFindPackageRevision(packages *porchapi.PackageRevisionList, name repository.PackageRevisionKey) *porchapi.PackageRevision {
	t.T().Helper()
	for i := range packages.Items {
		pr := &packages.Items[i]
		if pr.Spec.RepositoryName == name.RKey().Name &&
			pr.Spec.PackageName == name.PKey().Package &&
			pr.Spec.Revision == name.Revision {
			return pr
		}
	}
	t.Fatalf("Failed to find package %q", name)
	return nil
}

type PackageRevisionStatusCounts struct {
	Total, Draft, Proposed, Published, DeletionProposed int
}

func (c *PackageRevisionStatusCounts) String() string {
	total := c.Total
	drafts := c.Draft
	proposed := c.Proposed
	published := c.Published
	deletionProposed := c.DeletionProposed
	table := metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Total", Type: "int"},
			{Name: "Draft", Type: "int"},
			{Name: "Proposed", Type: "int"},
			{Name: "Published", Type: "int"},
			{Name: "Deletion Proposed", Type: "int"},
		},
		Rows: []metav1.TableRow{
			{
				Cells: []any{total, drafts, proposed, published, deletionProposed},
			},
		},
	}

	var buf bytes.Buffer
	p := printers.NewTablePrinter(printers.PrintOptions{})
	p.PrintObj((runtime.Object)(&table), &buf)
	return fmt.Sprintf("%+v", buf.String())
}

func (t *MultiClusterTestSuite) PackageRevisionCountsMustMatch(expected *PackageRevisionStatusCounts) {
	t.T().Helper()

	actual, err := t.CountPackageRevisions()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Actual package revision counts:\n\n%s\n", actual)

	// use assert.Equal for the individual lifecycle statuses so as to check all of them and
	// 		gather as much potentially-useful information as possible
	assert.Equal(t.T(), expected.Draft, actual.Draft, "Count of Draft PackageRevisions not as expected")
	assert.Equal(t.T(), expected.Proposed, actual.Proposed, "Count of Proposed PackageRevisions not as expected")
	assert.Equal(t.T(), expected.Published, actual.Published, "Count of Published PackageRevisions not as expected")
	assert.Equal(t.T(), expected.DeletionProposed, actual.DeletionProposed, "Count of DeletionProposed PackageRevisions not as expected")
	assert.Equal(t.T(), expected.Total, actual.Total, "Total count of PackageRevisions not as expected")

	// if failed, use require and Failed() to fail the entire test case at the end
	require.False(t.T(), t.T().Failed(), "Actual package revision counts not equal to expected counts")
}

func (t *TestSuite) CountPackageRevisions() (*PackageRevisionStatusCounts, error) {
	var packageRevisions porchapi.PackageRevisionList
	if err := t.Reader.List(t.GetContext(), &packageRevisions, client.InNamespace(t.Namespace)); err != nil {
		err := fmt.Errorf("error listing package revisions to count: %w", err)
		t.Logf(err.Error())
		return nil, err
	}

	counts := &PackageRevisionStatusCounts{}
	for _, each := range packageRevisions.Items {
		counts.Total += 1
		switch each.Spec.Lifecycle {
		case porchapi.PackageRevisionLifecycleDraft:
			counts.Draft += 1
		case porchapi.PackageRevisionLifecycleProposed:
			counts.Proposed += 1
		case porchapi.PackageRevisionLifecyclePublished:
			counts.Published += 1
		case porchapi.PackageRevisionLifecycleDeletionProposed:
			counts.DeletionProposed += 1
		}
	}
	return counts, nil
}
