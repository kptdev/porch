// Copyright 2026 The Nephio Authors
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

package crd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchv1alpha1 "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	aggregatorv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	porchTestRepo      = "porch-test"
	testBlueprintsRepo = "test-blueprints"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	scheme    *runtime.Scheme

	// shared namespace and context for all tests
	sharedNamespace string
	sharedCtx       context.Context

	// allInCluster is true when porch-server, repo-controller, and PR
	// controller are all running inside the cluster. When false, at least
	// one component is running locally and gitea must be reached via
	// LoadBalancer IP rather than cluster-internal DNS.
	allInCluster bool
)

func TestCRD(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("set E2E to run this test")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRD E2E Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	var err error

	scheme = runtime.NewScheme()
	Expect(porchv1alpha2.AddToScheme(scheme)).To(Succeed())
	Expect(porchv1alpha1.AddToScheme(scheme)).To(Succeed()) // for PackageRevisionResources
	Expect(configapi.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())
	Expect(aggregatorv1.AddToScheme(scheme)).To(Succeed())

	cfg, err = config.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	cfg.UserAgent = "porch-crd-e2e"

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	allInCluster = detectAllInCluster()
	GinkgoWriter.Printf("all components in-cluster: %v, gitea base URL: %s\n", allInCluster, giteaBaseURL())

	sharedCtx = context.Background()
	By("cleaning up stale namespaces from prior runs")
	var nsList corev1.NamespaceList
	Expect(k8sClient.List(sharedCtx, &nsList)).To(Succeed())
	for i := range nsList.Items {
		if strings.HasPrefix(nsList.Items[i].Name, "crd-e2e-") {
			// Remove finalizers from all PackageRevisions to unblock namespace deletion
			var prList porchv1alpha2.PackageRevisionList
			if err := k8sClient.List(sharedCtx, &prList, client.InNamespace(nsList.Items[i].Name)); err == nil {
				for j := range prList.Items {
					if len(prList.Items[j].Finalizers) > 0 {
						prList.Items[j].Finalizers = nil
						k8sClient.Update(sharedCtx, &prList.Items[j]) //nolint:errcheck
					}
				}
			}
			// Remove finalizers from repositories too
			var repoList configapi.RepositoryList
			if err := k8sClient.List(sharedCtx, &repoList, client.InNamespace(nsList.Items[i].Name)); err == nil {
				for j := range repoList.Items {
					if len(repoList.Items[j].Finalizers) > 0 {
						repoList.Items[j].Finalizers = nil
						k8sClient.Update(sharedCtx, &repoList.Items[j]) //nolint:errcheck
					}
				}
			}
			k8sClient.Delete(sharedCtx, &nsList.Items[i]) //nolint:errcheck
		}
	}

	sharedNamespace = fmt.Sprintf("crd-e2e-%d", time.Now().UnixMicro())

	By("creating shared namespace " + sharedNamespace)
	Expect(k8sClient.Create(sharedCtx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: sharedNamespace},
	})).To(Succeed())

	By("ensuring porch-test gitea repo exists")
	createGiteaRepo(porchTestRepo)

	By("registering porch-test")
	registerV1Alpha2Repo(sharedCtx, sharedNamespace, porchTestRepo)

	By("registering test-blueprints and waiting for discovery")
	registerV1Alpha2Repo(sharedCtx, sharedNamespace, testBlueprintsRepo)
	triggerRepoSync(sharedCtx, sharedNamespace, testBlueprintsRepo)
	waitForDiscovery(sharedCtx, sharedNamespace, crdName(testBlueprintsRepo, "basens", "v1"))
})

var _ = AfterSuite(func() {
	if sharedNamespace != "" {
		By("cleaning up shared namespace")
		k8sClient.Delete(sharedCtx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: sharedNamespace},
		})
	}
	By("resetting porch-test gitea repo to initial state")
	recreateGiteaRepo(porchTestRepo)
})

// detectAllInCluster returns true when porch-server and
// porch-controllers are all running inside the cluster.
//
// The porch-controllers deployment runs runs the controllers
// (repository, packagerevision, packagevariant, packagevariantset).
// If it's missing or scaled to 0, at least one controller
// is running locally and needs the LoadBalancer IP to reach gitea.
func detectAllInCluster() bool {
	if strings.ToLower(os.Getenv("RUN_E2E_LOCALLY")) == "true" {
		return false
	}

	ctx := context.Background()

	// Check porch-server: APIService → Service → has selector?
	apiSvc := &aggregatorv1.APIService{}
	err := k8sClient.Get(ctx, client.ObjectKey{
		Name: porchv1alpha1.SchemeGroupVersion.Version + "." + porchv1alpha1.SchemeGroupVersion.Group,
	}, apiSvc)
	if err != nil || apiSvc.Spec.Service == nil {
		return false
	}
	svc := &corev1.Service{}
	err = k8sClient.Get(ctx, client.ObjectKey{
		Namespace: apiSvc.Spec.Service.Namespace,
		Name:      apiSvc.Spec.Service.Name,
	}, svc)
	if err != nil || len(svc.Spec.Selector) == 0 {
		return false
	}

	// Check porch-controllers deployment. This runs the controllers
	// (repository, packagerevision, packagevariant, packagevariantset).
	// If missing or scaled to 0, controllers are running locally.
	deploy := &appsv1.Deployment{}
	err = k8sClient.Get(ctx, client.ObjectKey{
		Namespace: "porch-system",
		Name:      "porch-controllers",
	}, deploy)
	if err != nil {
		return false
	}
	return deploy.Spec.Replicas != nil && *deploy.Spec.Replicas > 0
}
