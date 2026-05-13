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

package performance

import (
	"flag"
	"fmt"
	"net/http"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	metricsPort = flag.Uint("port", 2113, "Port on which to expose metrics")
)

type PerformanceSuite struct {
	suiteutils.TestSuiteWithGit

	metricsServer   *http.Server
	metricsShutdown chan struct{}
}

func (t *PerformanceSuite) SetupSuite() {
	flag.Parse()
	t.TestSuiteWithGit.SetupSuite()
	t.metricsServer = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *metricsPort)}
	t.metricsShutdown = make(chan struct{})
	t.ServeMetrics()
}

func (t *PerformanceSuite) TearDownSuite() {
	t.ShutdownMetrics()
}

func (t *PerformanceSuite) ServeMetrics() {
	go func() {
		t.Logf("Starting metrics server")
		http.Handle("/metrics", promhttp.Handler())
		if err := t.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Fatalf("Error starting metrics server: %v", err)
		}
		t.Logf("Metrics server stopped")
		t.metricsShutdown <- struct{}{}
	}()
}

// ShutdownMetrics tries to gracefully shut down the metrics server
func (t *PerformanceSuite) ShutdownMetrics() {
	err := t.metricsServer.Shutdown(t.GetContext())
	if err != nil {
		t.Logf("Error shutting down metrics server: %v", err)
	}
	select {
	case <-t.metricsShutdown:
		t.Logf("Metrics server shutdown complete")
	case <-time.After(5 * time.Second):
		t.Logf("Metrics server shutdown timed out")
	}
}

func (t *PerformanceSuite) incrementGuage(obj client.Object) {
	if !t.T().Failed() {
		switch KindOf(obj) {
		case KindPackageRevision:
			if obj.(*porchapi.PackageRevision).Spec.Revision != -1 {
				packageRevisionGuage.Inc()
			}
		case KindRepository:
			repositoryGauge.Inc()
		}
	}
}

func (t *PerformanceSuite) decrementGuage(obj client.Object) {
	if !t.T().Failed() {
		switch KindOf(obj) {
		case KindPackageRevision:
			if obj.(*porchapi.PackageRevision).Spec.Revision != -1 {
				packageRevisionGuage.Dec()
			}
		case KindRepository:
			repositoryGauge.Dec()
		}
	}
}

func (t *PerformanceSuite) GetE(key client.ObjectKey, obj client.Object) {
	t.T().Helper()
	MeasureAndRecord(OperationGet, obj, func() { t.TestSuiteWithGit.GetE(key, obj) })
}

func (t *PerformanceSuite) GetF(key client.ObjectKey, obj client.Object) {
	t.T().Helper()
	MeasureAndRecord(OperationGet, obj, func() { t.TestSuiteWithGit.GetF(key, obj) })
}

func (t *PerformanceSuite) ListE(list client.ObjectList, opts ...client.ListOption) {
	t.T().Helper()
	MeasureAndRecord(OperationList, list, func() { t.TestSuiteWithGit.ListE(list, opts...) })
}

func (t *PerformanceSuite) ListF(list client.ObjectList, opts ...client.ListOption) {
	t.T().Helper()
	MeasureAndRecord(OperationList, list, func() { t.TestSuiteWithGit.ListF(list, opts...) })
}

func (t *PerformanceSuite) CreateF(obj client.Object, opts ...client.CreateOption) {
	t.T().Helper()
	MeasureAndRecord(OperationCreate, obj, func() { t.TestSuiteWithGit.CreateF(obj, opts...) })

	t.incrementGuage(obj)
}

func (t *PerformanceSuite) CreateE(obj client.Object, opts ...client.CreateOption) {
	t.T().Helper()
	MeasureAndRecord(OperationCreate, obj, func() { t.TestSuiteWithGit.CreateE(obj, opts...) })

	t.incrementGuage(obj)
}

func (t *PerformanceSuite) DeleteF(obj client.Object, opts ...client.DeleteOption) {
	t.T().Helper()
	MeasureAndRecord(OperationDelete, obj, func() { t.TestSuiteWithGit.DeleteF(obj, opts...) })

	t.decrementGuage(obj)
}

func (t *PerformanceSuite) DeleteE(obj client.Object, opts ...client.DeleteOption) {
	t.T().Helper()
	MeasureAndRecord(OperationDelete, obj, func() { t.TestSuiteWithGit.DeleteE(obj, opts...) })

	t.decrementGuage(obj)
}

func (t *PerformanceSuite) DeleteL(obj client.Object, opts ...client.DeleteOption) {
	t.T().Helper()
	hadError := false
	handler := func(format string, args ...any) {
		hadError = true
		t.Logf(format, args...)
	}
	MeasureAndRecord(OperationDelete, obj, func() { t.DeleteEH(obj, handler, opts...) })
	if !hadError {
		t.decrementGuage(obj)
	}
}

func (t *PerformanceSuite) UpdateF(obj client.Object, opts ...client.UpdateOption) {
	t.T().Helper()
	MeasureAndRecord(getUpdateOperation(obj), obj, func() { t.TestSuiteWithGit.UpdateF(obj, opts...) })
}

func (t *PerformanceSuite) UpdateE(obj client.Object, opts ...client.UpdateOption) {
	t.T().Helper()
	MeasureAndRecord(getUpdateOperation(obj), obj, func() { t.TestSuiteWithGit.UpdateE(obj, opts...) })
}

func getUpdateOperation(obj client.Object) Operation {
	if pr, ok := obj.(*porchapi.PackageRevision); ok && pr.Spec.Lifecycle == porchapi.PackageRevisionLifecycleProposed {
		return OperationPropose
	}
	return OperationUpdate
}

func (t *PerformanceSuite) PatchF(obj client.Object, patch client.Patch, opts ...client.PatchOption) {
	t.T().Helper()
	MeasureAndRecord(OperationPatch, obj, func() { t.TestSuiteWithGit.PatchF(obj, patch, opts...) })
}

func (t *PerformanceSuite) PatchE(obj client.Object, patch client.Patch, opts ...client.PatchOption) {
	t.T().Helper()
	MeasureAndRecord(OperationPatch, obj, func() { t.TestSuiteWithGit.PatchE(obj, patch, opts...) })
}

func (t *PerformanceSuite) UpdateApprovalL(pr *porchapi.PackageRevision) *porchapi.PackageRevision {
	t.T().Helper()
	var ret *porchapi.PackageRevision
	MeasureAndRecord(getUpdateApprovalOperation(pr), pr, func() { ret = t.TestSuiteWithGit.UpdateApprovalL(pr) })
	return ret
}

func (t *PerformanceSuite) UpdateApprovalF(pr *porchapi.PackageRevision) *porchapi.PackageRevision {
	t.T().Helper()
	var ret *porchapi.PackageRevision
	MeasureAndRecord(getUpdateApprovalOperation(pr), pr, func() { ret = t.TestSuiteWithGit.UpdateApprovalL(pr) })
	return ret
}

func getUpdateApprovalOperation(pr *porchapi.PackageRevision) Operation {
	switch pr.Spec.Lifecycle {
	case porchapi.PackageRevisionLifecyclePublished:
		return OperationPublish
	case porchapi.PackageRevisionLifecycleDeletionProposed:
		return OperationProposeDelete
	default:
		return OperationUpdateApproval
	}
}

// copied, so the operation is recorded
func (t *PerformanceSuite) CreatePackageDraftF(repository, packageName, workspace string) *porchapi.PackageRevision {
	t.T().Helper()
	pr := t.CreatePackageSkeleton(repository, packageName, workspace)
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeInit,
			Init: &porchapi.PackageInitTaskSpec{},
		},
	}
	t.CreateF(pr)
	return pr
}
