/*
 Copyright 2025 The kpt Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 You may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package crcache

import (
	"context"
	"testing"

	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/stretchr/testify/assert"
)

func TestPackage(t *testing.T) {
	assert.True(t, true)

	cachedPkg := cachedPackage{
		Package: &fake.FakePackage{},
	}
	assert.Equal(t, 0, cachedPkg.GetLatestRevision(context.TODO()))

	cachedPkg.latestPackageRevision = 123
	assert.Equal(t, 123, cachedPkg.GetLatestRevision(context.TODO()))
}
