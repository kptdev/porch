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

package del

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	rpkgutil "github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	command = "cmdrpkgdel"
)

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		Runner: rpkgutil.Runner{Ctx: ctx, Cfg: rcg},
	}
	c := &cobra.Command{
		Use:        "del PACKAGE",
		Aliases:    []string{"delete"},
		SuggestFor: []string{},
		Short:      docs.DelShort,
		Long:       docs.DelShort + "\n" + docs.DelLong,
		Example:    docs.DelExamples,
		PreRunE:    rpkgutil.MakePreRunE(command+".preRunE", rcg, &r.Client),
		RunE:       r.runE,
		Hidden:     cliutils.HidePorchCommands,
	}
	r.Command = c

	// Create flags

	return r
}

func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	v1 := newRunner(ctx, rcg)
	v2 := newV1Alpha2Runner(ctx, rcg)
	cliutils.WrapVersionDispatch(v1.Command, v2.preRunE, v2.runE)
	return v1.Command
}

type runner struct {
	rpkgutil.Runner
}

func (r *runner) runE(_ *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"
	if len(args) == 0 {
		return errors.E(op, fmt.Errorf("PACKAGE is a required positional argument"))
	}
	var messages []string

	for _, pkg := range args {
		pr := &porchapi.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchapi.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: *r.Cfg.Namespace,
				Name:      pkg,
			},
		}

		if err := r.Client.Delete(r.Ctx, pr); err != nil {
			messages = append(messages, err.Error())
			fmt.Fprintf(r.Command.ErrOrStderr(), "%s failed (%s)\n", pkg, err)
		} else {
			fmt.Fprintf(r.Command.OutOrStdout(), "%s deleted\n", pkg)
		}
	}

	if len(messages) > 0 {
		return errors.E(op, fmt.Errorf("errors:\n  %s", strings.Join(messages, "\n  ")))
	}

	return nil
}
