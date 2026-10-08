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

package podevaluator

import (
	"context"
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"
	distref "github.com/distribution/reference"
	"github.com/kptdev/kpt/pkg/fn/runtime"
	imageutil "github.com/kptdev/porch/pkg/util/image"
	"github.com/regclient/regclient"
	"github.com/regclient/regclient/scheme/reg"
	"k8s.io/klog/v2"
)

// warmupWildcardConstraint matches any release version when listing registry tags.
const warmupWildcardConstraint = ">= 0.0.0"

func (pm *podManager) newRegClientTagResolver() runtime.TagResolver {
	regclientOpts := []regclient.Opt{
		regclient.WithUserAgent("regclient/porch"),
		regclient.WithDockerCreds(),
	}

	if pm.tlsSecretPath != "" {
		var caCertPath string
		var caCert []byte
		var err error
		if caCertPath, err = tlsCACertPath(pm.tlsSecretPath); err == nil {
			caCert, err = os.ReadFile(caCertPath)
		}

		if err == nil {
			regclientOpts = append(regclientOpts, regclient.WithRegOpts(reg.WithCerts([][]byte{caCert})))
		} else {
			klog.Warningf("unable to read the CA certificate: %v", err)
		}
	}

	return runtime.TagResolver{
		Listers: []runtime.TagLister{
			&runtime.RegClientLister{Client: regclient.New(regclientOpts...)},
		},
	}
}

// regClientTagResolver returns a tag resolver backed by a new regclient instance so TLS
// credentials and other registry settings are read from disk on each call. When tagResolver
// has listers configured (unit tests), that resolver is returned instead.
func (pm *podManager) regClientTagResolver() runtime.TagResolver {
	if len(pm.tagResolver.Listers) > 0 {
		return pm.tagResolver
	}
	return pm.newRegClientTagResolver()
}

// resolvePodExecutorWarmupImage returns a fully qualified image reference for pod cache warmup.
//
// "*" lists registry tags and picks the highest release.
// An empty tag leaves the repository untagged.
// A semver version is copied on as a literal tag and does not list the registry.
// A semver constraint lists registry tags and picks the highest match.
// Any other valid OCI tag, such as "latest", is copied on as a literal tag.
func resolvePodExecutorWarmupImage(ctx context.Context, resolver runtime.TagResolver, repository, tag string) (string, error) {
	// 1. If tag is the wildcard, we accept any tag
	if tag == "*" {
		return resolver.ResolveFunctionImage(ctx, repository, warmupWildcardConstraint)
	}

	parsedImage := imageutil.Parse(repository)

	// 2. If tag is empty, return the repository with no tag.
	if tag == "" {
		parsedImage.Digest = ""
		parsedImage.Tag = ""
		return parsedImage.Full(), nil
	}

	// 3. If tag is a valid semver, we just substitute it
	if _, err := semver.NewVersion(tag); err == nil {
		parsedImage.Digest = ""
		parsedImage.Tag = tag
		return parsedImage.Full(), nil
	}

	// 4. If tag is a valid constraint, we resolve it
	if _, err := semver.NewConstraint(tag); err == nil {
		return resolver.ResolveFunctionImage(ctx, repository, tag)
	}

	// 5. If the tag is *not* a valid semver *or* constraint, we at least check if it is valid, then substitute it
	// we use FindString here since TagRegexp does not have start and end anchors (see imageWithLiteralTag in kpt)
	if distref.TagRegexp.FindString(tag) == tag {
		parsedImage.Digest = ""
		parsedImage.Tag = tag
		return parsedImage.Full(), nil
	}

	return "", fmt.Errorf("%q is not a valid tag", tag)
}
