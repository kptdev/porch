// Copyright 2022, 2024-2025 The kpt Authors
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

package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"
	"time"

	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/kptdev/kpt/pkg/oci"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

func (r *ociRepository) CreatePackageRevisionDraft(ctx context.Context, obj *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	base := empty.Image

	packageName := obj.Spec.PackageName
	ociRepo, err := name.NewRepository(path.Join(r.spec.Registry, packageName))
	if err != nil {
		return nil, err
	}

	if err := util.ValidPkgRevObjName(r.Key().Name, "/", packageName, string(obj.Spec.WorkspaceName)); err != nil {
		return nil, fmt.Errorf("failed to create packagerevision: %w", err)
	}

	// digestName := ImageDigestName{}
	return &ociPackageRevisionDraft{
		prKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Package: packageName,
			},
		},
		parent:    r,
		metadata:  obj.ObjectMeta,
		tasks:     []porchapi.Task{},
		base:      base,
		tag:       ociRepo.Tag(string(obj.Spec.WorkspaceName)),
		lifecycle: porchapi.PackageRevisionLifecycleDraft,
	}, nil
}

func (r *ociRepository) UpdatePackageRevision(ctx context.Context, old repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	oldPackage := old.(*ociPackageRevision)
	packageName := oldPackage.Key().PkgKey.Package
	workspace := oldPackage.Key().WorkspaceName
	// digestName := oldPackage.digestName

	ociRepo, err := name.NewRepository(path.Join(r.spec.Registry, packageName))
	if err != nil {
		return nil, err
	}

	// TODO: Authentication must be set up correctly. Do we use:
	// * Provided Service account?
	// * Workload identity?
	// * Caller credentials (is this possible with k8s apiserver)?
	options := []remote.Option{
		remote.WithAuthFromKeychain(gcrane.Keychain),
		remote.WithContext(ctx),
	}

	ref := ociRepo.Tag(string(workspace))

	base, err := remote.Image(ref, options...)
	if err != nil {
		return nil, fmt.Errorf("error fetching image %q: %w", ref, err)
	}

	return &ociPackageRevisionDraft{
		prKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Package: packageName,
			},
		},
		parent:    r,
		metadata:  old.GetMeta(),
		tasks:     []porchapi.Task{},
		base:      base,
		tag:       ref,
		lifecycle: oldPackage.Lifecycle(ctx),
	}, nil
}

type ociPackageRevisionDraft struct {
	prKey     repository.PackageRevisionKey
	parent    *ociRepository
	metadata  metav1.ObjectMeta
	tasks     []porchapi.Task
	base      v1.Image
	tag       name.Tag
	addendums []mutate.Addendum
	created   time.Time
	lifecycle porchapi.PackageRevisionLifecycle // New value of the package revision lifecycle
}

var _ repository.PackageRevisionDraft = (*ociPackageRevisionDraft)(nil)

func (p *ociPackageRevisionDraft) UpdateResources(ctx context.Context, new *porchapi.PackageRevisionResources, task *porchapi.Task) error {
	_, span := tracer.Start(ctx, "ociPackageRevisionDraft::UpdateResources", trace.WithAttributes())
	defer span.End()

	buf := bytes.NewBuffer(nil)
	writer := tar.NewWriter(buf)

	// TODO: write only changes.
	for k, v := range new.Spec.Resources {
		b := ([]byte)(v)
		blen := len(b)

		if err := writer.WriteHeader(&tar.Header{
			Name:       k,
			Size:       int64(blen),
			Mode:       0644,
			ModTime:    p.created,
			AccessTime: p.created,
			ChangeTime: p.created,
		}); err != nil {
			return fmt.Errorf("failed to write oci package tar header: %w", err)
		}

		if n, err := writer.Write(b); err != nil {
			return fmt.Errorf("failed to write oci package tar contents: %w", err)
		} else if n != blen {
			return fmt.Errorf("failed to write complete oci package tar content: %d of %d", n, blen)
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to finalize oci package tar content: %w", err)
	}

	layer := stream.NewLayer(io.NopCloser(buf), stream.WithCompressionLevel(gzip.BestCompression))
	if err := remote.WriteLayer(p.tag.Repository, layer, remote.WithAuthFromKeychain(gcrane.Keychain)); err != nil {
		return fmt.Errorf("failed to write remote layer: %w", err)
	}

	taskJSON, err := json.Marshal(*task)
	if err != nil {
		return fmt.Errorf("failed to marshal task %T to json: %w", task, err)
	}

	digest, err := layer.Digest()
	if err != nil {
		return fmt.Errorf("failed to get layer digets: %w", err)
	}

	remoteLayer, err := remote.Layer(
		p.tag.Context().Digest(digest.String()),
		remote.WithAuthFromKeychain(gcrane.Keychain))
	if err != nil {
		return fmt.Errorf("failed to create remote layer from digest: %w", err)
	}

	p.addendums = append(p.addendums, mutate.Addendum{
		Layer: remoteLayer,
		History: v1.History{
			Author:    "kool kat",
			Created:   v1.Time{Time: p.created},
			CreatedBy: "kpt:" + string(taskJSON),
		},
	})

	p.tasks = append(p.tasks, *task)

	return nil
}

func (p *ociPackageRevisionDraft) UpdateLifecycle(ctx context.Context, new porchapi.PackageRevisionLifecycle) error {
	p.lifecycle = new
	return nil
}

func (p *ociPackageRevisionDraft) Key() repository.PackageRevisionKey {
	return p.prKey
}

func (p *ociPackageRevisionDraft) GetMeta() metav1.ObjectMeta {
	return p.metadata
}

// Finish round of updates.
func (r *ociRepository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "ociRepository::ClosePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	p := prd.(*ociPackageRevisionDraft)

	ref := p.tag
	option := remote.WithAuthFromKeychain(gcrane.Keychain)

	klog.Infof("pushing %s", ref)

	revision := -1
	addendums := append([]mutate.Addendum{}, p.addendums...)
	if p.lifecycle != "" {
		if len(addendums) == 0 {
			return nil, fmt.Errorf("cannot create empty layer")
			// TODO: How do we create an empty layer ... failed to append image layers: unable to add a nil layer to the image
			// Maybe https://github.com/google/go-containerregistry/blob/fc6ff852e45e4bfd4fe41e03d992118687d3ec21/pkg/v1/static/static_test.go#L28-L29
			// addendums = append(addendums, mutate.Addendum{
			// 	Annotations: map[string]string{
			// 		annotationKeyLifecycle: string(p.lifecycle),
			// 	},
			// })
		} else {
			addendum := &addendums[len(addendums)-1]
			if addendum.Annotations == nil {
				addendum.Annotations = make(map[string]string)
			}
			addendum.Annotations[annotationKeyLifecycle] = string(p.lifecycle)

			if porchapi.LifecycleIsPublished(p.lifecycle) {
				r := p.parent
				// Finalize the package revision. Assign it a revision number of latest + 1.
				revisions, err := r.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
					Key: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							Package: p.Key().PkgKey.Package,
						},
					},
				})
				if err != nil {
					return nil, err
				}

				highestRev := -1
				for _, rev := range revisions {
					if porchapi.LifecycleIsPublished(rev.Lifecycle(ctx)) && rev.Key().Revision > highestRev {
						highestRev = rev.Key().Revision
					}
				}
				revision = highestRev + 1
				addendum.Annotations[annotationKeyRevision] = repository.Revision2Str(revision)
			}
		}
	}

	base := p.base
	if base == nil {
		base = empty.Image
	}
	img, err := mutate.Append(base, addendums...)
	if err != nil {
		return nil, fmt.Errorf("failed to append image layers: %w", err)
	}

	// TODO: We have a race condition here; there's no way to indicate that we want to create / not update an existing tag
	if err := remote.Write(ref, img, option); err != nil {
		return nil, fmt.Errorf("failed to push image %s: %w", ref, err)
	}

	// TODO: remote.Write should return the digest etc that was pushed
	desc, err := remote.Get(ref, option)
	if err != nil {
		return nil, fmt.Errorf("error getting metadata for %s: %w", ref, err)
	}
	klog.Infof("desc %s", string(desc.Manifest))

	digestName := oci.ImageDigestName{
		Image:  ref.Name(),
		Digest: desc.Digest.String(),
	}

	configFile, err := p.parent.storage.CachedConfigFile(ctx, digestName)
	if err != nil {
		return nil, fmt.Errorf("error getting config file: %w", err)
	}

	return p.parent.buildPackageRevision(ctx, digestName, p.Key().PkgKey.Package, p.tag.TagStr(), revision, configFile.Created.Time)
}

func constructResourceVersion(t time.Time) string {
	return strconv.FormatInt(t.UnixNano(), 10)
}

func constructUID(ref string) types.UID {
	return types.UID("uid:" + ref)
}

func (r *ociRepository) DeletePackageRevision(ctx context.Context, old repository.PackageRevision) error {
	oldPackage := old.(*ociPackageRevision)
	packageName := oldPackage.Key().PkgKey.Package
	workspace := oldPackage.Key().WorkspaceName

	ociRepo, err := name.NewRepository(path.Join(r.spec.Registry, packageName))
	if err != nil {
		return err
	}

	// TODO: Authentication must be set up correctly. Do we use:
	// * Provided Service account?
	// * Workload identity?
	// * Caller credentials (is this possible with k8s apiserver)?
	options := []remote.Option{
		remote.WithAuthFromKeychain(gcrane.Keychain),
		remote.WithContext(ctx),
	}

	ref := ociRepo.Tag(string(workspace))

	klog.Infof("deleting %s", ref)

	if err := remote.Delete(ref, options...); err != nil {
		return fmt.Errorf("error deleting image %q: %w", ref, err)
	}

	return nil
}
