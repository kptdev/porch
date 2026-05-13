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

package git

import (
	"github.com/go-git/go-git/v5/plumbing"
)

type commitOperation struct {
	opType string
	data   interface{}
}

type commitOperationBuilder struct {
	operations []commitOperation
}

func newCommitOperationBuilder() *commitOperationBuilder {
	return &commitOperationBuilder{
		operations: []commitOperation{},
	}
}

func (c *commitOperationBuilder) addPackageApproval(draft interface{}, tag plumbing.ReferenceName) {
	c.operations = append(c.operations, commitOperation{
		opType: "approval",
		data:   map[string]interface{}{"draft": draft, "tag": tag},
	})
}

func (c *commitOperationBuilder) addPackageDeletion(branch plumbing.ReferenceName, prKey interface{}) {
	c.operations = append(c.operations, commitOperation{
		opType: "deletion",
		data:   map[string]interface{}{"branch": branch, "prKey": prKey},
	})
}

func (c *commitOperationBuilder) getOperations() []commitOperation {
	return c.operations
}
