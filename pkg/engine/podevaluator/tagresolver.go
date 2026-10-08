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
// An empty tag and "latest" are passed through to ResolveFunctionImage, matching evaluation:
// empty leaves the repository untagged, and "latest" stays a literal image tag.
// "*" lists registry tags and picks the highest release.
func resolvePodExecutorWarmupImage(ctx context.Context, resolver runtime.TagResolver, repository, tag string) (string, error) {
	switch tag {
	case "*":
		return resolver.ResolveFunctionImage(ctx, repository, warmupWildcardConstraint)
	case "", "latest":
		return resolver.ResolveFunctionImage(ctx, repository, tag)
	default:
		if _, err := semver.NewVersion(tag); err == nil {
			parsedImage := imageutil.Parse(repository)
			parsedImage.Tag = ""
			parsedImage.Digest = ""
			parsedImage.Tag = tag
			return parsedImage.Full(), nil
		}
		if _, err := semver.NewConstraint(tag); err == nil {
			return resolver.ResolveFunctionImage(ctx, repository, tag)
		}
		return "", fmt.Errorf("tag %q is not latest, a wildcard, a strict semver or a semver constraint", tag)
	}
}
