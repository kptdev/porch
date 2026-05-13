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

package util

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
)

func TestNewFactory(t *testing.T) {
	cmd := &cobra.Command{}
	version := "v1.0"

	factory := NewFactory(cmd, version)

	assert.NotNil(t, factory, "Expected a non-nil cluster.Factory")
	assert.NotNil(t, cmd.PersistentFlags(), "Expected PersistentFlags to be set on the command")

	_, err := cmd.PersistentFlags().GetString("kubeconfig")
	assert.NoError(t, err, "Expected kubeconfig flag to be added without error")
}

func TestUpdateQPSFlowControlDisabled(t *testing.T) {
	flags := genericclioptions.NewConfigFlags(true)
	UpdateQPS(flags)

	mockConfig := &rest.Config{
		QPS:   5,
		Burst: 10,
	}
	updatedConfig := flags.WrapConfigFn(mockConfig)

	// Since flow control is disabled, expect QPS and Burst to be increased to the minimum values (30, 60).
	assert.Equal(t, float32(30), updatedConfig.QPS, "Expected QPS to be updated to 30")
	assert.Equal(t, 60, updatedConfig.Burst, "Expected Burst to be updated to 60")
}
