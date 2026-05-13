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

import (
	"os"

	"github.com/spf13/cobra"
)

const (
	APIVersionV1Alpha1 = "v1alpha1"
	APIVersionV1Alpha2 = "v1alpha2"
	EnvAPIVersion      = "PORCHCTL_API_VERSION"
	FlagAPIVersion     = "api-version"
)

// GetAPIVersion returns the API version from the command's persistent flag
// or the PORCHCTL_API_VERSION environment variable. Defaults to v1alpha1.
func GetAPIVersion(cmd *cobra.Command) string {
	// Look up the flag in the full flag set (includes inherited persistent flags).
	if f := cmd.Flags().Lookup(FlagAPIVersion); f != nil && f.Changed && f.Value.String() != "" {
		return f.Value.String()
	}
	// Also check parent persistent flags directly (for pre-Execute lookups).
	if f := cmd.InheritedFlags().Lookup(FlagAPIVersion); f != nil && f.Changed && f.Value.String() != "" {
		return f.Value.String()
	}
	if v := os.Getenv(EnvAPIVersion); v != "" {
		return v
	}
	return APIVersionV1Alpha1
}

// IsV1Alpha2 is a convenience check.
func IsV1Alpha2(cmd *cobra.Command) bool {
	return GetAPIVersion(cmd) == APIVersionV1Alpha2
}
