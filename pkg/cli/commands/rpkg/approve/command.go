// Copyright 2022,2026 The kpt Authors
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

package approve

import (
	"context"
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	rpkgutil "github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	command = "cmdrpkgapprove"
)

// NewCommand returns the cobra command for `rpkg approve`, which
// transitions a package revision into the Published lifecycle state
// after readiness gates have been satisfied.
func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	v1 := newRunner(ctx, rcg)
	v2 := newV1Alpha2Runner(ctx, rcg)
	cliutils.WrapVersionDispatch(v1.Command, v2.preRunE, v2.runE)
	return v1.Command
}

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		Runner: rpkgutil.Runner{Ctx: ctx, Cfg: rcg},
	}

	c := &cobra.Command{
		Use:     "approve PACKAGE",
		Short:   docs.ApproveShort,
		Long:    docs.ApproveShort + "\n" + docs.ApproveLong,
		Example: docs.ApproveExamples,
		PreRunE: rpkgutil.MakePreRunE(command+".preRunE", rcg, &r.Client),
		RunE:    r.runE,
		Hidden:  cliutils.HidePorchCommands,
	}
	r.Command = c

	return r
}

type runner struct {
	rpkgutil.Runner
}

func approveAction(ctx context.Context, client client.Client, pr *porchapi.PackageRevision) (string, error) {
	if err := cliutils.UpdatePackageRevisionApproval(ctx, client, pr, porchapi.PackageRevisionLifecyclePublished); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s approved", pr.Name), nil
}

func (r *runner) runE(_ *cobra.Command, args []string) error {
	return rpkgutil.RunForEachPackage(r.Ctx, r.Client, r.Command, *r.Cfg.Namespace, args,
		rpkgutil.RunForEachOpts{CmdName: command, WithRetry: true, CheckReadiness: true},
		approveAction)
}
