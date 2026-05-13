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

package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

type pushRefSpecBuilder struct {
	pushRefs map[plumbing.ReferenceName]plumbing.Hash
	require  map[plumbing.ReferenceName]plumbing.Hash
}

func newPushRefSpecBuilder() *pushRefSpecBuilder {
	return &pushRefSpecBuilder{
		pushRefs: map[plumbing.ReferenceName]plumbing.Hash{},
		require:  map[plumbing.ReferenceName]plumbing.Hash{},
	}
}

func (b *pushRefSpecBuilder) addRefToPush(hash plumbing.Hash, to plumbing.ReferenceName) {
	b.pushRefs[to] = hash
}

func (b *pushRefSpecBuilder) addRefToDelete(ref *plumbing.Reference) {
	b.addRefToPush(plumbing.ZeroHash, ref.Name())
	b.requireRef(ref)
}

func (b *pushRefSpecBuilder) requireRef(ref *plumbing.Reference) {
	if ref != nil {
		b.require[ref.Name()] = ref.Hash()
	}
}

func (b *pushRefSpecBuilder) updateRequiredRefs(repo *git.Repository) {
	for refName := range b.require {
		// Try local ref first, then remote ref
		if ref, err := repo.Reference(refName, true); err == nil {
			b.require[refName] = ref.Hash()
		} else {
			// Try remote equivalent
			remoteName := plumbing.ReferenceName("refs/remotes/origin/" + refName.Short())
			if ref, err := repo.Reference(remoteName, true); err == nil {
				b.require[refName] = ref.Hash()
			}
		}
	}
}

func (b *pushRefSpecBuilder) buildRefSpecs() (push []config.RefSpec, require []config.RefSpec, err error) {
	for local, hash := range b.pushRefs {
		remote, err := refInRemoteFromRefInLocal(local)
		if err != nil {
			return nil, nil, err
		}

		if !hash.IsZero() {
			push = append(push, config.RefSpec(fmt.Sprintf("%s:%s", hash, remote)))
		} else {
			push = append(push, config.RefSpec(fmt.Sprintf(":%s", remote)))
		}
	}

	for local, hash := range b.require {
		remote, err := refInRemoteFromRefInLocal(local)
		if err != nil {
			return nil, nil, err
		}
		if !hash.IsZero() {
			require = append(require, config.RefSpec(fmt.Sprintf("%s:%s", hash, remote)))
		}
	}

	return push, require, nil
}
