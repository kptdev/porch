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

package deps

import (
	"context"
	"fmt"
	"os"

	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const command = "cmdrpkgdeps"

// NewCommand returns the cobra command for `rpkg deps`.
//
// Dependency projection is a v1alpha2-only feature (the fields it reads exist
// only on v1alpha2 PackageRevisions), so this command always uses the v1alpha2
// client regardless of --api-version. An explicit --api-version=v1alpha1 is
// rejected rather than silently ignored.
func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	v2 := newV1Alpha2Runner(ctx, rcg)

	cmd := &cobra.Command{
		Use:     "deps K8S_PACKAGE_REV_NAME [flags]",
		Short:   docs.DepsShort,
		Long:    docs.DepsShort + "\n" + docs.DepsLong,
		Example: docs.DepsExamples,
		Args:    cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// deps always runs against v1alpha2. Reject an explicit v1alpha1
			// request (via flag or env); an unset version defaults to v1alpha2 here.
			if explicitlyV1Alpha1(cmd) {
				return fmt.Errorf("rpkg deps is only available for --api-version=%s", cliutils.APIVersionV1Alpha2)
			}
			return v2.preRunE(cmd, args)
		},
		RunE:   v2.runE,
		Hidden: cliutils.HidePorchCommands,
	}
	v2.cmd = cmd

	cmd.Flags().BoolVar(&v2.dependents, "dependents", false,
		"List downstream package revisions that depend on the named package (top-down). Default is to list the package's own upstreams (bottom-up).")
	cmd.Flags().BoolVar(&v2.canDelete, "can-delete", false,
		"Exit non-zero if the named package has any dependents. Implies --dependents.")

	return cmd
}

// explicitlyV1Alpha1 reports whether the user explicitly requested v1alpha1
// (via the --api-version flag or the PORCHCTL_API_VERSION env var). An unset
// version is treated as v1alpha2 for this command.
func explicitlyV1Alpha1(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup(cliutils.FlagAPIVersion); f != nil && f.Changed {
		return f.Value.String() == cliutils.APIVersionV1Alpha1
	}
	if f := cmd.InheritedFlags().Lookup(cliutils.FlagAPIVersion); f != nil && f.Changed {
		return f.Value.String() == cliutils.APIVersionV1Alpha1
	}
	return os.Getenv(cliutils.EnvAPIVersion) == cliutils.APIVersionV1Alpha1
}
