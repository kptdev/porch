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

package crcache

import (
	"context"
	"sync"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/crcache/meta"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// We take advantage of the cache having a global view of all the packages
// in a repository and compute the latest package revision in the cache
// rather than add another level of caching in the repositories themselves.
// This also reuses the revision comparison code and ensures same behavior
// between Git and OCI.

var _ repository.PackageRevision = &cachedPackageRevision{}

type cachedPackageRevision struct {
	repository.PackageRevision
	metadataStore    meta.MetadataStore
	isLatestRevision bool
	mutex            sync.Mutex
}

func (c *cachedPackageRevision) KubeObjectName() string {
	return repository.ComposePkgRevObjName(c.Key())
}

func (c *cachedPackageRevision) KubeObjectNamespace() string {
	return c.Key().RKey().Namespace
}

func (c *cachedPackageRevision) UID() types.UID {
	return util.GenerateUid("packagerevision:", c.KubeObjectNamespace(), c.KubeObjectName())
}

func (c *cachedPackageRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cachedPackageRevision::GetPackageRevision", trace.WithAttributes())
	defer span.End()

	apiPR, err := c.PackageRevision.GetPackageRevision(ctx)
	if err != nil {
		return nil, err
	}

	apiPR.Annotations = c.GetMeta().Annotations
	apiPR.Finalizers = c.GetMeta().Finalizers
	apiPR.OwnerReferences = c.GetMeta().OwnerReferences
	apiPR.DeletionTimestamp = c.GetMeta().DeletionTimestamp
	apiPR.Labels = c.GetMeta().Labels
	c.mutex.Lock()
	latest := c.isLatestRevision
	c.mutex.Unlock()
	if latest {
		// copy the labels in case the cached object is being read by another go routine
		labels := make(map[string]string, len(apiPR.Labels))
		for k, v := range apiPR.Labels {
			labels[k] = v
		}
		labels[porchapi.LatestPackageRevisionKey] = porchapi.LatestPackageRevisionValue
		apiPR.Labels = labels
	}

	return apiPR, nil
}

func (c *cachedPackageRevision) SetMeta(ctx context.Context, pkgRevMeta metav1.ObjectMeta) error {
	storedMeta, err := c.metadataStore.Get(ctx, types.NamespacedName{
		Name:      c.KubeObjectName(),
		Namespace: c.KubeObjectNamespace(),
	})
	if err != nil {
		return err
	}

	if !util.CompareObjectMeta(pkgRevMeta, storedMeta) {
		if storedMeta, err = c.metadataStore.Update(ctx, pkgRevMeta); err != nil {
			return err
		}
	}

	return c.PackageRevision.SetMeta(ctx, storedMeta)
}

func (c *cachedPackageRevision) IsLatestRevision() bool {
	return c.isLatestRevision
}

func (c *cachedPackageRevision) GetCommitInfo() (time.Time, string) {
	return c.PackageRevision.GetCommitInfo()
}
