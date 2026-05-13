// Copyright 2025 The kpt Authors
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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPackageCUD(t *testing.T) {

	testPackages := packages{}

	pkgObj, err := testPackages.Create(context.TODO(), nil, nil, nil)
	assert.Nil(t, pkgObj)
	assert.Equal(t, "create or clone a PackageRevision resource to create a package as direct package creation is not supported on resources of kind \"packages.porch.kpt.dev\"", err.Error())

	pkgObj, flag, err := testPackages.Update(context.TODO(), "", nil, nil, nil, false, nil)
	assert.Nil(t, pkgObj)
	assert.False(t, flag)
	assert.Equal(t, "update a PackageRevision resource to update a package as direct package update is not supported on resources of kind \"packages.porch.kpt.dev\"", err.Error())

	pkgObj, flag, err = testPackages.Delete(context.TODO(), "", nil, nil)
	assert.Nil(t, pkgObj)
	assert.False(t, flag)
	assert.Equal(t, "delete all PackageRevisions resources of a package to delete a package as direct package deletion is not supported on resources of kind \"packages.porch.kpt.dev\"", err.Error())
}
