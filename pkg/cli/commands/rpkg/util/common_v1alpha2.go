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

package util

import (
	"context"
	"strconv"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PackageAlreadyExistsV1Alpha2 reports whether any revision of packageName exists in repository.
// It asks the API server for at most one match instead of listing every PackageRevision.
func PackageAlreadyExistsV1Alpha2(ctx context.Context, c client.Client, repository, packageName, namespace string) (bool, error) {
	matches, err := ListV1Alpha2PackageRevisionsByFields(ctx, c, namespace, client.MatchingFields{
		string(porchv1alpha2.PkgRevSelectorRepository):  repository,
		string(porchv1alpha2.PkgRevSelectorPackageName): packageName,
	}, 1)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// ListV1Alpha2PackageRevisionsByFields lists the PackageRevisions in namespace whose selectable
// fields (see porchv1alpha2.PackageRevisionSelectableFields) equal the given values, filtered by
// the API server. limit > 0 caps the number returned. If the server rejects the field selector
// (CRD field selectors need Kubernetes 1.31+), it falls back to listing the namespace and
// filtering here.
func ListV1Alpha2PackageRevisionsByFields(ctx context.Context, c client.Client, namespace string,
	fields client.MatchingFields, limit int64) ([]porchv1alpha2.PackageRevision, error) {
	var out []porchv1alpha2.PackageRevision
	continueToken := ""
	for {
		opts := []client.ListOption{client.InNamespace(namespace), fields}
		if limit > 0 {
			// A limited List may return fewer items (even none) plus a continue token.
			opts = append(opts, client.Limit(limit-int64(len(out))))
		}
		if continueToken != "" {
			opts = append(opts, client.Continue(continueToken))
		}
		var list porchv1alpha2.PackageRevisionList
		if err := c.List(ctx, &list, opts...); err != nil {
			if !apierrors.IsBadRequest(err) {
				return nil, err
			}
			// Field selector not supported by this API server: list the namespace and filter locally.
			return listAndFilterV1Alpha2(ctx, c, namespace, fields, limit)
		}
		out = append(out, list.Items...)
		continueToken = list.Continue
		if continueToken == "" || (limit > 0 && int64(len(out)) >= limit) {
			return out, nil
		}
	}
}

func listAndFilterV1Alpha2(ctx context.Context, c client.Client, namespace string,
	fields client.MatchingFields, limit int64) ([]porchv1alpha2.PackageRevision, error) {
	var list porchv1alpha2.PackageRevisionList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	var out []porchv1alpha2.PackageRevision
	for i := range list.Items {
		if !matchesV1Alpha2Fields(&list.Items[i], fields) {
			continue
		}
		out = append(out, list.Items[i])
		if limit > 0 && int64(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func matchesV1Alpha2Fields(pr *porchv1alpha2.PackageRevision, fields client.MatchingFields) bool {
	for field, want := range fields {
		got, ok := V1Alpha2PackageRevisionFieldValue(pr, field)
		if !ok || got != want {
			return false
		}
	}
	return true
}

// V1Alpha2PackageRevisionFieldValue returns the value of a PackageRevision selectable field, as the
// API server compares it in a field selector. ok is false for fields that are not selectable.
func V1Alpha2PackageRevisionFieldValue(pr *porchv1alpha2.PackageRevision, field string) (string, bool) {
	switch porchv1alpha2.PkgRevFieldSelector(field) {
	case porchv1alpha2.PkgRevSelectorName:
		return pr.Name, true
	case porchv1alpha2.PkgRevSelectorNamespace:
		return pr.Namespace, true
	case porchv1alpha2.PkgRevSelectorRepository:
		return pr.Spec.RepositoryName, true
	case porchv1alpha2.PkgRevSelectorPackageName:
		return pr.Spec.PackageName, true
	case porchv1alpha2.PkgRevSelectorWorkspaceName:
		return pr.Spec.WorkspaceName, true
	case porchv1alpha2.PkgRevSelectorLifecycle:
		return string(pr.Spec.Lifecycle), true
	case porchv1alpha2.PkgRevSelectorRevision:
		return strconv.Itoa(pr.Status.Revision), true
	default:
		return "", false
	}
}
