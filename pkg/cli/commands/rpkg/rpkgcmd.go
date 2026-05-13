// Copyright 2022, 2026 The kpt Authors
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

package rpkg

import (
	"context"
	"flag"
	"fmt"

	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/approve"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/clone"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/copy"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/del"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/get"
	initialization "github.com/kptdev/porch/pkg/cli/commands/rpkg/init"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/propose"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/proposedelete"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/pull"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/push"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/reject"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/upgrade"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
)

func NewCommand(ctx context.Context, version string) *cobra.Command {
	rpkg := &cobra.Command{
		Use:     "rpkg",
		Aliases: []string{"rpackage"},
		Short:   docs.RpkgShort,
		Long:    docs.RpkgLong,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := cmd.Flags().GetBool("help")
			if err != nil {
				return err
			}
			if h {
				return cmd.Help()
			}
			return cmd.Usage()
		},
		Hidden: cliutils.HidePorchCommands,
	}

	pf := rpkg.PersistentFlags()

	kubeflags := genericclioptions.NewConfigFlags(true)
	kubeflags.AddFlags(pf)

	kubeflags.WrapConfigFn = func(rc *rest.Config) *rest.Config {
		rc.UserAgent = fmt.Sprintf("porchctl/%s", version)
		return rc
	}

	pf.String(cliutils.FlagAPIVersion, "",
		"API version for PackageRevision (v1alpha1 or v1alpha2). Default: v1alpha1. Env: PORCHCTL_API_VERSION")

	pf.AddGoFlagSet(flag.CommandLine)

	rpkg.AddCommand(
		get.NewCommand(ctx, kubeflags),
		pull.NewCommand(ctx, kubeflags),
		push.NewCommand(ctx, kubeflags),
		clone.NewCommand(ctx, kubeflags),
		initialization.NewCommand(ctx, kubeflags),
		propose.NewCommand(ctx, kubeflags),
		approve.NewCommand(ctx, kubeflags),
		reject.NewCommand(ctx, kubeflags),
		del.NewCommand(ctx, kubeflags),
		copy.NewCommand(ctx, kubeflags),
		upgrade.NewCommand(ctx, kubeflags),
		proposedelete.NewCommand(ctx, kubeflags),
	)

	return rpkg
}
