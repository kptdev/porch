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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/google"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	"github.com/kptdev/kpt/pkg/oci"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

type ociRepository struct {
	key        repository.RepositoryKey
	spec       configapi.OciRepository
	deployment bool

	storage *oci.Storage
}

var _ repository.Repository = &ociRepository{}

func (r *ociRepository) KubeObjectNamespace() string {
	return r.Key().Namespace
}

func (r *ociRepository) KubeObjectName() string {
	return r.Key().Name
}

func (r *ociRepository) Key() repository.RepositoryKey {
	return r.key
}

func (r *ociRepository) Close(context.Context) error {
	return nil
}

// there is probably a more efficient way to do this
func (r *ociRepository) Version(ctx context.Context) (string, error) {
	ctx, span := tracer.Start(ctx, "ociRepository::Version")
	defer span.End()

	ociRepo, err := name.NewRepository(r.spec.Registry)
	if err != nil {
		return "", err
	}

	options := r.storage.CreateOptions(ctx)

	tags, err := google.List(ociRepo, options...)
	if err != nil {
		return "", err
	}

	klog.Infof("tags: %#v", tags)

	b := bytes.Buffer{}
	for _, childName := range tags.Children {
		path := fmt.Sprintf("%s/%s", r.spec.Registry, childName)
		child, err := name.NewRepository(path, name.StrictValidation)
		if err != nil {
			klog.Warningf("Cannot create nested repository %q: %v", path, err)
			continue
		}

		childTags, err := google.List(child, options...)
		if err != nil {
			klog.Warningf("Cannot list nested repository %q: %v", path, err)
			continue
		}

		// klog.Infof("childTags: %#v", childTags)

		for digest, m := range childTags.Manifests {
			b.WriteString(digest)
			mb, err := m.MarshalJSON()
			if err != nil {
				return "", err
			}
			b.Write(mb)
		}
	}
	hash := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(hash[:]), nil
}

func (r *ociRepository) BranchCommitHash(ctx context.Context) (string, error) {
	// OCI repositories don't have branches or commits
	return "", nil
}

func (r *ociRepository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "ociRepository::ListPackageRevisions")
	defer span.End()

	ociRepo, err := name.NewRepository(r.spec.Registry)
	if err != nil {
		return nil, err
	}

	options := r.storage.CreateOptions(ctx)

	tags, err := google.List(ociRepo, options...)
	if err != nil {
		return nil, err
	}

	klog.Infof("tags: %#v", tags)

	var result []repository.PackageRevision
	for _, childName := range tags.Children {
		path := fmt.Sprintf("%s/%s", r.spec.Registry, childName)
		child, err := name.NewRepository(path, name.StrictValidation)
		if err != nil {
			klog.Warningf("Cannot create nested repository %q: %v", path, err)
			continue
		}

		childTags, err := google.List(child, options...)
		if err != nil {
			klog.Warningf("Cannot list nested repository %q: %v", path, err)
			continue
		}

		// klog.Infof("childTags: %#v", childTags)

		for digest, m := range childTags.Manifests {
			for _, tag := range m.Tags {
				created := m.Created
				if created.IsZero() {
					created = m.Uploaded
				}

				// ref := child.Tag(tag)
				// ref := child.Digest(digest)

				p := &ociPackageRevision{
					// tagName: ImageTagName{
					// 	Image: child.Name(),
					// 	Tag:   tag,
					// },
					digestName: oci.ImageDigestName{
						Image:  child.Name(),
						Digest: digest,
					},
					prKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							Package: childName,
						},

						WorkspaceName: tag,
					},
					created:         created,
					parent:          r,
					resourceVersion: constructResourceVersion(m.Created),
				}
				p.uid = constructUID(p.Key().PkgKey.Package + ":" + string(p.Key().WorkspaceName))

				lifecycle, err := r.getLifecycle(ctx, p.digestName)
				if err != nil {
					return nil, err
				}
				p.lifecycle = lifecycle

				revision, err := r.getRevisionNumber(ctx, p.digestName)
				if err != nil {
					return nil, err
				}
				p.prKey.Revision = revision

				tasks, err := r.loadTasks(ctx, p.digestName)
				if err != nil {
					return nil, err
				}
				p.tasks = tasks

				if filter.Matches(ctx, p) {
					result = append(result, p)
				}
			}
		}
	}

	return result, nil
}

func (r *ociRepository) ListPackages(ctx context.Context, filter repository.ListPackageFilter) ([]repository.Package, error) {
	return nil, fmt.Errorf("ListPackages not supported for OCI packages")
}

func (r *ociRepository) buildPackageRevision(ctx context.Context, name oci.ImageDigestName, packageName, workspace string,
	revision int, created time.Time) (repository.PackageRevision, error) {

	ctx, span := tracer.Start(ctx, "ociRepository::buildPackageRevision")
	defer span.End()

	// for backwards compatibility with packages that existed before porch supported
	// workspaces, we populate the workspaceName as the revision number if it is empty
	if workspace == "" {
		workspace = repository.Revision2Str(revision)
	}

	p := &ociPackageRevision{
		digestName: name,
		prKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Package: packageName,
			},
			WorkspaceName: workspace,
			Revision:      revision,
		},
		created:         created,
		parent:          r,
		resourceVersion: constructResourceVersion(created),
	}
	p.uid = constructUID(p.Key().PkgKey.Package + ":" + string(p.Key().WorkspaceName))

	lifecycle, err := r.getLifecycle(ctx, p.digestName)
	if err != nil {
		return nil, err
	}
	p.lifecycle = lifecycle

	tasks, err := r.loadTasks(ctx, p.digestName)
	if err != nil {
		return nil, err
	}
	p.tasks = tasks

	return p, nil
}

func (r *ociRepository) Refresh(_ context.Context) error {
	return nil
}

// ToMainPackageRevision implements repository.PackageRevision.
func (p *ociPackageRevision) ToMainPackageRevision(context.Context) repository.PackageRevision {
	panic("unimplemented")
}

type ociPackageRevision struct {
	prKey           repository.PackageRevisionKey
	digestName      oci.ImageDigestName
	created         time.Time
	resourceVersion string
	uid             types.UID
	metadata        metav1.ObjectMeta

	parent *ociRepository
	tasks  []porchapi.Task

	lifecycle porchapi.PackageRevisionLifecycle
}

var _ repository.PackageRevision = &ociPackageRevision{}

func (c *ociPackageRevision) KubeObjectName() string {
	return repository.ComposePkgRevObjName(c.Key())
}

func (c *ociPackageRevision) KubeObjectNamespace() string {
	return c.Key().RKey().Namespace
}

func (c *ociPackageRevision) UID() types.UID {
	return util.GenerateUid("packagerevision:", c.KubeObjectNamespace(), c.KubeObjectName())
}

func (p *ociPackageRevision) GetResources(ctx context.Context) (*porchapi.PackageRevisionResources, error) {
	resources, err := LoadResources(ctx, p.parent.storage, &p.digestName)
	if err != nil {
		return nil, err
	}

	key := p.Key()

	return &porchapi.PackageRevisionResources{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionResources",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: p.KubeObjectNamespace(),
			Name:      p.KubeObjectName(),
			CreationTimestamp: metav1.Time{
				Time: p.metadata.CreationTimestamp.Time,
			},
			ResourceVersion: p.resourceVersion,
			UID:             p.uid,
		},
		Spec: porchapi.PackageRevisionResourcesSpec{
			PackageName:    key.PkgKey.Package,
			WorkspaceName:  key.WorkspaceName,
			Revision:       key.Revision,
			RepositoryName: key.RKey().Name,

			Resources: resources.Contents,
		},
	}, nil
}

func (p *ociPackageRevision) ResourceVersion() string {
	return p.resourceVersion
}

func (p *ociPackageRevision) Key() repository.PackageRevisionKey {
	return p.prKey
}

func (p *ociPackageRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "ociPackageRevision::GetPackageRevision", trace.WithAttributes())
	defer span.End()

	key := p.Key()

	kf, err := p.GetKptfile(ctx)
	if err != nil {
		return nil, err
	}

	return &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: p.KubeObjectName(),
			Name:      p.KubeObjectName(),
			CreationTimestamp: metav1.Time{
				Time: p.metadata.CreationTimestamp.Time,
			},
			ResourceVersion: p.resourceVersion,
			UID:             p.uid,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    key.PkgKey.Package,
			RepositoryName: key.RKey().Name,
			Revision:       key.Revision,
			WorkspaceName:  key.WorkspaceName,

			Lifecycle:      p.Lifecycle(ctx),
			Tasks:          p.tasks,
			ReadinessGates: repository.ToAPIReadinessGates(kf),
			PackageMetadata: &porchapi.PackageMetadata{
				Labels:      kf.Labels,
				Annotations: kf.Annotations,
			},
		},
		Status: porchapi.PackageRevisionStatus{
			// TODO:        UpstreamLock,
			// TODO:        SelfLock,
			Deployment: p.parent.deployment,
			Conditions: repository.ToAPIConditions(kf),
		},
	}, nil
}

func (p *ociPackageRevision) GetKptfile(ctx context.Context) (kptfilev1.KptFile, error) {
	resources, err := LoadResources(ctx, p.parent.storage, &p.digestName)
	if err != nil {
		return kptfilev1.KptFile{}, fmt.Errorf("error loading package resources for %v: %w", p.digestName, err)
	}
	kfString, found := resources.Contents[kptfilev1.KptFileName]
	if !found {
		return kptfilev1.KptFile{}, fmt.Errorf("packagerevision does not have a Kptfile")
	}
	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(kfString))
	if err != nil {
		return kptfilev1.KptFile{}, fmt.Errorf("error decoding Kptfile: %w", err)
	}
	return *kf, nil
}

func (p *ociPackageRevision) GetUpstreamLock(context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("upstreamLock is not supported for OCI packages (%s)", p.KubeObjectName())
}

func (p *ociPackageRevision) GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	_, span := tracer.Start(ctx, "ociPackageRevision::GetLock", trace.WithAttributes())
	defer span.End()
	return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("lock is not supported for oci packages (%s)", p.KubeObjectName())
}

func (p *ociPackageRevision) Lifecycle(ctx context.Context) porchapi.PackageRevisionLifecycle {
	return p.lifecycle
}

// UpdateLifecycle should update the package revision lifecycle from DeletionProposed to Published or vice versa.
//
//	This function is currently only partially implemented; it still needs to store whether the package has been
//	proposed for deletion somewhere in OCI, probably as another OCI image with a "deletionProposed" tag.
func (p *ociPackageRevision) UpdateLifecycle(ctx context.Context, new porchapi.PackageRevisionLifecycle) error {
	ctx, span := tracer.Start(ctx, "ociPackageRevision::UpdateLifecycle", trace.WithAttributes())
	defer span.End()

	old := p.Lifecycle(ctx)

	if old == porchapi.PackageRevisionLifecyclePublished {
		if new != porchapi.PackageRevisionLifecycleDeletionProposed {
			return fmt.Errorf("invalid new lifecycle value: %q", new)
		}

		// TODO: Create a "deletionProposed" OCI image tag.
		p.lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	}
	if old == porchapi.PackageRevisionLifecycleDeletionProposed {
		if new != porchapi.PackageRevisionLifecyclePublished {
			return fmt.Errorf("invalid new lifecycle value: %q", new)
		}

		// TODO: Delete the "deletionProposed" OCI image tag.
		p.lifecycle = porchapi.PackageRevisionLifecyclePublished
	}
	return nil
}

func (p *ociPackageRevision) GetMeta() metav1.ObjectMeta {
	return p.metadata
}

func (p *ociPackageRevision) SetMeta(_ context.Context, metadata metav1.ObjectMeta) error {
	p.metadata = metadata
	return nil
}

func (p *ociPackageRevision) GetCommitInfo() (time.Time, string) {
	return time.Time{}, ""
}

func (p *ociPackageRevision) IsLatestRevision() bool {
	return false
}
