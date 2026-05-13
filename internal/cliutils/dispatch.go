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

package porch

import "github.com/spf13/cobra"

// RunFunc is the signature for cobra PreRunE/RunE.
type RunFunc func(cmd *cobra.Command, args []string) error

// WrapVersionDispatch wraps a cobra.Command so that PreRunE and RunE
// dispatch to v1alpha2 implementations when --api-version=v1alpha2.
// The original (v1alpha1) PreRunE/RunE are preserved as the default path.
func WrapVersionDispatch(cmd *cobra.Command, v2PreRunE, v2RunE RunFunc) {
	origPreRunE := cmd.PreRunE
	origRunE := cmd.RunE

	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if IsV1Alpha2(cmd) {
			if v2PreRunE != nil {
				return v2PreRunE(cmd, args)
			}
			return nil
		}
		if origPreRunE != nil {
			return origPreRunE(cmd, args)
		}
		return nil
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if IsV1Alpha2(cmd) {
			return v2RunE(cmd, args)
		}
		return origRunE(cmd, args)
	}
}
