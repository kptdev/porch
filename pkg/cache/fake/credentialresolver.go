// Copyright 2024 The kpt Authors
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

package fake

import (
	"context"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/kptdev/porch/pkg/repository"
)

type credential struct {
	cabundle string
}

func (c *credential) ToString() string {
	return c.cabundle
}

func (c *credential) Valid() bool {
	return true
}

func (c *credential) ToAuthMethod() transport.AuthMethod {
	panic("unimplemented")
}

type CredentialResolver struct {
	cabundle string
}

func (cr *CredentialResolver) ResolveCredential(ctx context.Context, namespace, name string) (repository.Credential, error) {
	return &credential{
		cabundle: cr.cabundle,
	}, nil
}
