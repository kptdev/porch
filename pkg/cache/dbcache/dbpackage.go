// Copyright 2024-2025 The kpt Authors
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

package dbcache

import (
	"context"
	"database/sql"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
)

var _ repository.Package = &dbPackage{}

type dbPackage struct {
	repo      *dbRepository
	pkgKey    repository.PackageKey
	meta      metav1.ObjectMeta
	spec      *porchapi.PackageSpec
	updated   time.Time
	updatedBy string
}

func (p *dbPackage) KubeObjectName() string {
	return repository.ComposePkgObjName(p.Key())
}

func (p *dbPackage) KubeObjectNamespace() string {
	return p.Key().RKey().Namespace
}

func (p *dbPackage) UID() types.UID {
	return util.GenerateUid("dbPackage:", p.KubeObjectNamespace(), p.KubeObjectName())
}

func (p *dbPackage) Key() repository.PackageKey {
	return p.pkgKey
}

func (p *dbPackage) savePackage(ctx context.Context) (*dbPackage, error) {
	_, span := tracer.Start(ctx, "dbPackage:savePackage", trace.WithAttributes())
	defer span.End()

	_, err := pkgReadFromDB(ctx, p.Key())
	if err == nil {
		return p, pkgUpdateDB(ctx, p)
	} else if err != sql.ErrNoRows {
		return p, err
	}

	p.meta = metav1.ObjectMeta{
		Name:      p.KubeObjectName(),
		Namespace: p.KubeObjectNamespace(),
	}

	p.spec = &porchapi.PackageSpec{
		PackageName:    p.pkgKey.Package,
		RepositoryName: p.Key().RKey().Name,
	}

	return p, pkgWriteToDB(ctx, p)
}

func (p *dbPackage) GetPackage(ctx context.Context) *porchapi.PorchPackage {
	_, span := tracer.Start(ctx, "dbPackage:GetPackage", trace.WithAttributes())
	defer span.End()

	key := p.Key()

	return &porchapi.PorchPackage{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PorchPackage",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            p.KubeObjectName(),
			Namespace:       p.Key().RKey().Namespace,
			UID:             p.UID(),
			ResourceVersion: "undefined",
			CreationTimestamp: metav1.Time{
				Time: p.updated,
			},
		},
		Spec: porchapi.PackageSpec{
			PackageName:    key.Package,
			RepositoryName: key.RKey().Name,
		},
		Status: porchapi.PackageStatus{
			LatestRevision: p.GetLatestRevision(ctx),
		},
	}
}

func (p *dbPackage) savePackageRevision(ctx context.Context, d *dbPackageRevision, saveResources bool) (*dbPackageRevision, error) {
	_, span := tracer.Start(ctx, "dbPackage:savePackageRevision", trace.WithAttributes())
	defer span.End()

	return d.savePackageRevision(ctx, saveResources)
}

func (p *dbPackage) DeletePackageRevision(ctx context.Context, old repository.PackageRevision, deleteExternal bool) error {
	_, span := tracer.Start(ctx, "dbPackage:DeletePackageRevision", trace.WithAttributes())
	defer span.End()

	dbPR := old.(*dbPackageRevision)
	if err := dbPR.Delete(ctx, deleteExternal); err != nil {
		return err
	}

	prSlice, err := pkgRevReadPRsFromDB(ctx, p.Key())
	if err != nil {
		return err
	}

	if len(prSlice) == 0 {
		return pkgDeleteFromDB(ctx, p.Key())
	}

	if dbPR.IsLatestRevision() {
		klog.Infof("dbPackage %+v: latest PackageRevision deleted. Sending notification.", p.Key())
		go p.sendLatestPkgUpdateNotification()
	}

	return nil
}

// sendLatestPkgUpdateNotification sends async notification when a new latest package revision is identified
func (p *dbPackage) sendLatestPkgUpdateNotification() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, span := tracer.Start(ctx, "dbPackage::sendLatestPkgUpdateNotification", trace.WithAttributes())
	defer span.End()

	latestRevision, err := pkgRevReadLatestPRFromDB(ctx, p.Key())
	if err != nil {
		klog.Error(err)
		return
	} else if latestRevision == nil {
		klog.Infof("dbPackage %+v: no new latest PackageRevision found. Notification not sent.", p.Key())
		return
	}

	sent := p.repo.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Modified, latestRevision)
	klog.Infof("dbcache: sent %d for latest PackageRevision %s/%s", sent, latestRevision.KubeObjectNamespace(), latestRevision.KubeObjectName())

}

func (p *dbPackage) GetLatestRevision(ctx context.Context) int {
	_, span := tracer.Start(ctx, "dbPackage:GetLatestRevision", trace.WithAttributes())
	defer span.End()

	if latestRev, err := pkgRevGetlatestRevFromDB(ctx, p.Key()); err == nil {
		return latestRev
	} else {
		klog.Error(err)
		return 0
	}
}

func (p *dbPackage) Delete(ctx context.Context, deleteExternal bool) error {
	_, span := tracer.Start(ctx, "dbPackage:Delete", trace.WithAttributes())
	defer span.End()

	dbPkgRevs, err := pkgRevReadPRsFromDB(ctx, p.Key())
	if err != nil {
		return err
	}

	for _, pkgRev := range dbPkgRevs {
		if err := pkgRev.Delete(ctx, deleteExternal); err != nil {
			return err
		}
	}

	return pkgDeleteFromDB(ctx, p.Key())
}
