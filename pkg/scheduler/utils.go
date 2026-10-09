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

package scheduler

import (
	"context"
	"fmt"
	"hash/crc32"
	iofs "io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"strings"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/pkg/metrics"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// Engine options

func (rs *RenderScheduler) SetRuntime(runtime fn.FunctionRuntime) {
	rs.runtime = runtime
}

func (rs *RenderScheduler) SetRunnerOptions(runnerOptionsResolver func(namespace string) runneroptions.RunnerOptions) {
	rs.runnerOptions = runnerOptionsResolver("")
}

// runnerOptionsFor copies the scheduler options and sets the kpt package-name
// format to repo.%s.workspace. Workers render concurrently, so the shared options stay unchanged.
func (rs *RenderScheduler) runnerOptionsFor(prKey repository.PackageRevisionKey) runneroptions.RunnerOptions {
	opts := rs.runnerOptions
	opts.LogOptions.PkgNameFormat = fmt.Sprintf("%s.%%s.%s", prKey.PkgKey.RepoKey.Name, prKey.WorkspaceName)
	return opts
}

func (rs *RenderScheduler) SetKubeClient(kubeClient client.Client) {
	rs.kubeClient = kubeClient
}

// General utils

// RenderExecutionStatus returns the current status of the render operation for the given PackageRevision.
func (rs *RenderScheduler) RenderExecutionStatus(pr repository.PackageRevision) RenderExecutionStatus {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if _, exists := rs.ongoing[pr.Key()]; exists {
		return RenderStatusOngoing
	}
	if _, exists := rs.scheduled[pr.Key()]; exists {
		return RenderStatusScheduled
	}
	return RenderStatusUnknown
}

func hasKptfile(resources map[string]string) bool {
	contents, ok := resources[kptfileapi.KptFileName]
	return ok && len(contents) > 0
}

func wrapCancelFunc(cancel context.CancelFunc, prName string) context.CancelFunc {
	return func() {
		// use pkg/errors to create stack trace
		traceErr := pkgerrors.New("stack trace:")
		klog.Infof("Cancel called when rendering %q, %+v", prName, traceErr)
		cancel()
	}
}

// WithRenderTmpDir sets the parent directory for per-render scratch directories.
// Empty keeps the OS default temp dir. Renders must not use a small memory-backed
// tmpfs: input, function output and new files all coexist while kpt writes results back.
func WithRenderTmpDir(dir string) RenderSchedulerOption {
	return func(o *RenderSchedulerOptions) {
		o.RenderTmpDir = dir
	}
}

// Resource and File Ops

// computeResourceHash computes a deterministic CRC32 checksum of a resource map for
// conflict detection. Keys are sorted before hashing to ensure determinism.
func computeResourceHash(resources map[string]string) uint32 {
	keys := slices.Sorted(maps.Keys(resources))

	h := crc32.NewIEEE()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(resources[k]))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum32()
}

func createTempDir(tempDir string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp(tempDir, "porch-render-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			klog.Warningf("failed to remove render temp directory %q: %v", dir, removeErr)
		}
	}
	return dir, cleanup, nil
}

func relativeResourcePath(root, filePath string) string {
	rel := strings.TrimPrefix(filePath, root)
	return strings.TrimPrefix(rel, "/")
}

func WriteResources(fs filesys.FileSystem, resources repository.PackageResources, root string) (string, error) {
	var packageDir string // path to the topmost directory containing Kptfile
	for k, v := range resources.Contents {
		relDir := path.Dir(k)
		absDir := root
		if relDir != "." {
			absDir = path.Join(root, relDir)
		}

		if err := fs.MkdirAll(absDir); err != nil {
			return "", err
		}

		base := path.Base(k)
		if err := fs.WriteFile(path.Join(absDir, base), []byte(v)); err != nil {
			return "", err
		}

		if base == kptfileapi.KptFileName {
			// Found Kptfile. Check if the current directory is ancestor of the current
			// topmost package directory. If so, use it instead.
			if packageDir == "" || absDir == root || strings.HasPrefix(packageDir, absDir+"/") {
				packageDir = absDir
			}
		}
	}
	// Return topmost directory containing Kptfile
	return packageDir, nil
}

func ReadResources(fs filesys.FileSystem, root string) (repository.PackageResources, error) {
	contents := map[string]string{}

	if err := fs.Walk(root, func(filePath string, info iofs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			data, err := fs.ReadFile(filePath)
			if err != nil {
				return err
			}
			contents[relativeResourcePath(root, filePath)] = string(data)
		}
		return nil
	}); err != nil {
		return repository.PackageResources{}, err
	}

	return repository.PackageResources{
		Contents: contents,
	}, nil
}

// Large Package Render Options and utils

func WithWorkQueueSize(n int) RenderSchedulerOption {
	return func(o *RenderSchedulerOptions) {
		if n > 0 {
			o.WorkQueueSize = n
		}
	}
}

func WithWorkerNum(n int) RenderSchedulerOption {
	return func(o *RenderSchedulerOptions) {
		if n > 0 {
			o.WorkerNum = n
		}
	}
}

func WithLargePackageThreshold(bytes int64) RenderSchedulerOption {
	return func(o *RenderSchedulerOptions) {
		if bytes > 0 {
			o.LargePackageThreshold = bytes
		}
	}
}

func WithMaxConcurrentLargeRenders(n int) RenderSchedulerOption {
	return func(o *RenderSchedulerOptions) {
		if n > 0 {
			o.MaxConcurrentLargeRenders = n
		}
	}
}

func packageRevisionSizeBytes(ctx context.Context, pr repository.PackageRevision) int64 {
	if pr == nil {
		return 0
	}
	apiPR, err := pr.GetPackageRevision(ctx, false)
	if err != nil {
		klog.Warningf("could not read PackageRevision status for render admission: %v", err)
		return 0
	}
	if apiPR == nil {
		return 0
	}
	return apiPR.Status.ResourcesSizeBytes
}

func renderRequestName(req RenderExecutionRequest) string {
	if req.pr == nil {
		return ""
	}
	return req.pr.Key().K8SName()
}

func (rs *RenderScheduler) isLarge(sizeBytes int64) bool {
	return sizeBytes >= rs.largePackageBytes
}

func (rs *RenderScheduler) removeLargeWaitlistedPr(prKey repository.PackageRevisionKey) {
	if len(rs.largeWaitlist) == 0 {
		return
	}
	kept := rs.largeWaitlist[:0]
	for _, req := range rs.largeWaitlist {
		if req.pr != nil && req.pr.Key() == prKey {
			continue
		}
		kept = append(kept, req)
	}
	rs.largeWaitlist = kept
}

func (rs *RenderScheduler) releaseLargeRender(req RenderExecutionRequest) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.isLarge(req.sizeBytes) && rs.largeRendersOngoing > 0 {
		rs.largeRendersOngoing--
	}
	rs.promoteWaitlistLocked()
}

func (rs *RenderScheduler) promoteWaitlistLocked() {
	for len(rs.largeWaitlist) > 0 && rs.largeRendersOngoing < rs.maxConcurrentLargeRenders {
		next := rs.largeWaitlist[0]
		rs.largeWaitlist = rs.largeWaitlist[1:]
		if next.ctx != nil {
			select {
			case <-next.ctx.Done():
				klog.Infof("Dropping cancelled waitlisted large render for %q", renderRequestName(next))
				continue
			default:
			}
		}
		select {
		case rs.workQueue <- next:
			rs.largeRendersOngoing++
			klog.Infof("Promoting waitlisted large render for %q", renderRequestName(next))
		default:
			rs.largeWaitlist = append([]RenderExecutionRequest{next}, rs.largeWaitlist...)
			metrics.RecordQueueSize("RenderSchedulerLargeWaitlist", float64(len(rs.largeWaitlist)))
			return
		}
	}
	metrics.RecordQueueSize("RenderSchedulerLargeWaitlist", float64(len(rs.largeWaitlist)))
}
