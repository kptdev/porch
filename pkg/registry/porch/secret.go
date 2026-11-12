// Copyright 2022 The kpt Authors
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

package porch

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/kptdev/porch/pkg/repository"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Values for secret types supported by porch.
	BasicAuthType = core.SecretTypeBasicAuth

	// Secret.Data key required for the caBundle
	CaBundleDataName = "ca.crt"

	// Secret.Data key required for bearer token authentication
	TokenDataName = "bearerToken"
)

func NewCredentialResolver(coreClient client.Reader, resolverChain []Resolver) repository.CredentialResolver {
	return &secretResolver{
		coreClient:    coreClient,
		resolverChain: resolverChain,
	}
}

type secretResolver struct {
	resolverChain []Resolver
	coreClient    client.Reader
}

type Resolver interface {
	Resolve(ctx context.Context, secret core.Secret) (repository.Credential, bool, error)
}

var _ repository.CredentialResolver = &secretResolver{}

func (r *secretResolver) ResolveCredential(ctx context.Context, namespace, name string) (repository.Credential, error) {
	var secret core.Secret
	if err := r.coreClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, &secret); err != nil {
		return nil, fmt.Errorf("cannot resolve credentials in a secret %s/%s: %w", namespace, name, err)
	}

	for _, resolver := range r.resolverChain {
		cred, found, err := resolver.Resolve(ctx, secret)
		if err != nil {
			return nil, fmt.Errorf("error resolving credential: %w", err)
		}
		if found {
			return cred, nil
		}
	}
	return nil, &NoMatchingResolverError{
		Type: string(secret.Type),
	}
}

type NoMatchingResolverError struct {
	Type string
}

func (e *NoMatchingResolverError) Error() string {
	return fmt.Sprintf("no resolver for secret with type %s", e.Type)
}

func (e *NoMatchingResolverError) Is(err error) bool {
	nmre, ok := err.(*NoMatchingResolverError)
	if !ok {
		return false
	}
	return nmre.Type == e.Type
}

func NewBasicAuthResolver() Resolver {
	return &BasicAuthResolver{}
}

var _ Resolver = &BasicAuthResolver{}

type BasicAuthResolver struct{}

func (b *BasicAuthResolver) Resolve(_ context.Context, secret core.Secret) (repository.Credential, bool, error) {
	if secret.Type != BasicAuthType {
		return nil, false, nil
	}

	return &BasicAuthCredential{
		Username: string(secret.Data["username"]),
		Password: string(secret.Data["password"]),
	}, true, nil
}

type BasicAuthCredential struct {
	Username string
	Password string // #nosec G117
}

func (b *BasicAuthCredential) ToString() string {
	panic("unimplemented")
}

var _ repository.Credential = &BasicAuthCredential{}

func (b *BasicAuthCredential) Valid() bool {
	return true
}

func (b *BasicAuthCredential) ToAuthMethod() transport.AuthMethod {
	return &http.BasicAuth{
		Username: string(b.Username),
		Password: string(b.Password),
	}
}

// Bearer Token Secret Verification
func NewBearerTokenAuthResolver() Resolver {
	return &BearerTokenAuthResolver{}
}

var _ Resolver = &BearerTokenAuthResolver{}

type BearerTokenAuthResolver struct{}

type BearerTokenAuthCredentials struct {
	BearerToken string // #nosec G117
}

func (b *BearerTokenAuthResolver) Resolve(_ context.Context, secret core.Secret) (repository.Credential, bool, error) {
	if secret.Type != core.SecretTypeOpaque {
		return nil, false, fmt.Errorf("bearer Token secret.Type value must be Opaque")
	} else {
		if secret.Data[TokenDataName] == nil {
			return nil, false, fmt.Errorf("bearer Token secret.Data key must be set as %s", TokenDataName)
		}
	}

	return &BearerTokenAuthCredentials{
		BearerToken: string(secret.Data[TokenDataName]),
	}, true, nil
}

func (b *BearerTokenAuthCredentials) ToString() string {
	return b.BearerToken
}

var _ repository.Credential = &BearerTokenAuthCredentials{}

func (b *BearerTokenAuthCredentials) Valid() bool {
	return true
}

func (b *BearerTokenAuthCredentials) ToAuthMethod() transport.AuthMethod {
	return &http.TokenAuth{
		Token: string(b.BearerToken),
	}
}

func NewCaBundleResolver() Resolver {
	return &CaBundleResolver{}
}

var _ Resolver = &CaBundleResolver{}

type CaBundleResolver struct{}

func (c *CaBundleResolver) Resolve(_ context.Context, secret core.Secret) (repository.Credential, bool, error) {
	if secret.Data[CaBundleDataName] == nil {
		return nil, false, fmt.Errorf("CaBundle secret.Data key must be set as %s", CaBundleDataName)
	}

	return &CaBundleCredential{
		CaBundle: string(secret.Data[CaBundleDataName]),
	}, true, nil
}

type CaBundleCredential struct {
	CaBundle string
}

func (c *CaBundleCredential) ToString() string {
	return c.CaBundle
}

var _ repository.Credential = &CaBundleCredential{}

func (c *CaBundleCredential) Valid() bool {
	return true
}

func (c *CaBundleCredential) ToAuthMethod() transport.AuthMethod {
	panic("unimplemented")
}
