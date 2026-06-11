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

package image

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindBestSemverMatch(t *testing.T) {
	testCases := map[string]struct {
		constraint  string
		tags        []string
		expected    string
		expectedErr string
	}{
		"selects highest matching version": {
			constraint: ">= 0.4.0 < 0.5.0",
			tags: []string{
				"v0.4.1",
				"v0.4",
				"@sha256:abcdef123456",
			},
			expected: "v0.4.1",
		},
		"exact version match": {
			constraint: "0.1.1",
			tags: []string{
				"v0.1.1",
				"v0.1",
			},
			expected: "v0.1.1",
		},
		"no matching version for valid constraint": {
			constraint: "> 1.0.0",
			tags: []string{
				"v0.1.1",
				"v0.1",
			},
			expectedErr: "no tag matching",
		},
		"invalid semver constraint": {
			constraint: ">> 1.0.0",
			tags: []string{
				"v1.1.0",
			},
			expectedErr: "invalid semver constraint",
		},
		"skips sha256-tagged entries": {
			constraint: ">= 0.4.0",
			tags: []string{
				"v0.4.1",
				"v0.4",
				"@sha256:abcdef123456",
			},
			expected: "v0.4.1",
		},
		"matches without registry prefix": {
			constraint: ">= 0.4.0",
			tags: []string{
				"v0.4.1",
				"v0.4",
				"@sha256:abcdef123456",
			},
			expected: "v0.4.1",
		},
		"empty cache keys": {
			constraint:  ">= 0.1.0",
			tags:        []string{},
			expectedErr: "no tag matching",
		},
		"selects greatest from multiple matches": {
			constraint: ">= 1.0.0 < 2.0.0",
			tags: []string{
				"v1.0.0",
				"v1.1.0",
				"v1.2.0",
				"v2.0.0",
			},
			expected: "v1.2.0",
		},
		"skips entries with unparseable versions": {
			constraint: ">= 1.0.0",
			tags: []string{
				"v1.0.0",
				"vnotaversion",
			},
			expected: "v1.0.0",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			best, err := FindBestSemverMatch(tc.constraint, tc.tags)
			if tc.expectedErr != "" {
				assert.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, best)
			}
		})
	}
}

func TestImageParse(t *testing.T) {
	const (
		registry         = "ghcr.io"
		registryWithPort = "my-registry.com:5000"
		subpath          = "kptdev/krm-functions-catalog"
		image            = "apply-setters"
		tag              = "v0.2.3"
		digest           = "sha256:7d89a74f106241391f687fc2985c8e6de597bb21f0d0014def5edc730618d9cc"
	)

	testCases := map[string]struct {
		input string
		want  ParsedImage
	}{
		"empty": {
			input: "",
			want:  ParsedImage{},
		},
		"base name only": {
			input: image,
			want: ParsedImage{
				Original: image,
				BaseName: image,
			},
		},
		"tag only": {
			input: fmt.Sprintf("%s:%s", image, tag),
			want: ParsedImage{
				Original: fmt.Sprintf("%s:%s", image, tag),
				BaseName: image,
				Tag:      tag,
			},
		},
		"registry no path": {
			input: fmt.Sprintf("%s/%s", registry, image),
			want: ParsedImage{
				Original: fmt.Sprintf("%s/%s", registry, image),
				Registry: registry,
				BaseName: image,
			},
		},
		"registry with path": {
			input: fmt.Sprintf("%s/%s:%s", subpath, image, tag),
			want: ParsedImage{
				Original: fmt.Sprintf("%s/%s:%s", subpath, image, tag),
				SubPath:  subpath,
				BaseName: image,
				Tag:      tag,
			},
		},
		"fully qualified, no digest": {
			input: fmt.Sprintf("%s/%s/%s:%s", registry, subpath, image, tag),
			want: ParsedImage{
				Original: fmt.Sprintf("%s/%s/%s:%s", registry, subpath, image, tag),
				Registry: registry,
				SubPath:  subpath,
				BaseName: image,
				Tag:      tag,
			},
		},
		"digest without tag": {
			input: fmt.Sprintf("%s@%s", image, digest),
			want: ParsedImage{
				Original: fmt.Sprintf("%s@%s", image, digest),
				BaseName: image,
				Digest:   digest,
			},
		},
		"tag and digest": {
			input: fmt.Sprintf("%s:%s@%s", image, tag, digest),
			want: ParsedImage{
				Original: fmt.Sprintf("%s:%s@%s", image, tag, digest),
				BaseName: image,
				Tag:      tag,
				Digest:   digest,
			},
		},
		"registry with port": {
			input: fmt.Sprintf("%s/%s/%s:%s", registryWithPort, subpath, image, tag),
			want: ParsedImage{
				Original: fmt.Sprintf("%s/%s/%s:%s", registryWithPort, subpath, image, tag),
				Registry: registryWithPort,
				SubPath:  subpath,
				BaseName: image,
				Tag:      tag,
			},
		},
		"digest with nested repository": {
			input: fmt.Sprintf("%s/%s/%s@%s", registry, subpath, image, digest),
			want: ParsedImage{
				Original: fmt.Sprintf("%s/%s/%s@%s", registry, subpath, image, digest),
				Registry: registry,
				SubPath:  subpath,
				BaseName: image,
				Digest:   digest,
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got := Parse(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
