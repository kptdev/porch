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

package packagerevision

import (
	"sort"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

// extractSubpackageUpstreams returns resolved upstreams from nested Kptfiles,
// sorted by path. The root Kptfile is skipped (reported via Status.UpstreamLock).
// truncated is true if capped at MaxSubpackageUpstreams. Malformed Kptfiles are
// skipped and returned in parseErrs rather than failing the whole computation.
func extractSubpackageUpstreams(resources map[string]string) (out []porchv1alpha2.SubpackageUpstream, truncated bool, parseErrs []string) {
	for key, content := range resources {
		if !strings.HasSuffix(key, "/"+kptfilev1.KptFileName) {
			continue // nested Kptfiles only; root has no "/" prefix
		}
		kf, err := kptfileutil.DecodeKptfile(strings.NewReader(content))
		if err != nil {
			parseErrs = append(parseErrs, key)
			continue
		}
		if kf.UpstreamLock == nil || kf.UpstreamLock.Git == nil {
			continue // no resolved upstream
		}
		out = append(out, porchv1alpha2.SubpackageUpstream{
			Path:     strings.TrimSuffix(key, "/"+kptfilev1.KptFileName),
			Upstream: porchv1alpha2.KptLocatorToLocator(*kf.UpstreamLock),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	if len(out) > porchv1alpha2.MaxSubpackageUpstreams {
		out = out[:porchv1alpha2.MaxSubpackageUpstreams]
		truncated = true
	}
	return out, truncated, parseErrs
}
