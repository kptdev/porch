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

package reject

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	command = "cmdrpkgreject"
)

func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	v1 := newRunner(ctx, rcg)
	v2 := newV1Alpha2Runner(ctx, rcg)
	cliutils.WrapVersionDispatch(v1.Command, v2.preRunE, v2.runE)
	return v1.Command
}

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		ctx: ctx,
		cfg: rcg,
	}

	c := &cobra.Command{
		Use:     "reject PACKAGE",
		Short:   docs.RejectShort,
		Long:    docs.RejectShort + "\n" + docs.RejectLong,
		Example: docs.RejectExamples,
		PreRunE: r.preRunE,
		RunE:    r.runE,
		Hidden:  cliutils.HidePorchCommands,
	}
	r.Command = c

	return r
}

type runner struct {
	ctx     context.Context
	cfg     *genericclioptions.ConfigFlags
	client  client.Client
	Command *cobra.Command

	// Flags
}

func (r *runner) preRunE(_ *cobra.Command, args []string) error {
	const op errors.Op = command + ".preRunE"

	if len(args) < 1 {
		return errors.E(op, "PACKAGE_REVISION is a required positional argument")
	}

	client, err := cliutils.CreateClientWithFlags(r.cfg)
	if err != nil {
		return errors.E(op, err)
	}
	r.client = client
	return nil
}

func (r *runner) runE(_ *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"
	var messages []string

	namespace := *r.cfg.Namespace
	var proposedFor string
	for _, name := range args {
		key := client.ObjectKey{
			Namespace: namespace,
			Name:      name,
		}
		var lastErr error
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var pr porchapi.PackageRevision
			if err := r.client.Get(r.ctx, key, &pr); err != nil {
				lastErr = err
				return err
			}
			switch pr.Spec.Lifecycle {
			case porchapi.PackageRevisionLifecycleProposed:
				proposedFor = "approval"
				err := cliutils.UpdatePackageRevisionApproval(r.ctx, r.client, &pr, porchapi.PackageRevisionLifecycleDraft)
				if err == nil {
					lastErr = nil
				} else {
					lastErr = err
				}
				return err
			case porchapi.PackageRevisionLifecycleDeletionProposed:
				proposedFor = "deletion"
				// NOTE(kispaljr): should we use UpdatePackageRevisionApproval() here?
				pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
				err := r.client.Update(r.ctx, &pr)
				if err == nil {
					lastErr = nil
				} else {
					lastErr = err
				}
				return err
			default:
				lastErr = fmt.Errorf("cannot reject %s with lifecycle '%s'", name, pr.Spec.Lifecycle)
				return lastErr
			}
		})
		// Workaround for k8s retry library bug: OnError/RetryOnConflict sometimes returns nil even when errors occur
		if err == nil && lastErr != nil {
			err = lastErr
		}
		if err != nil {
			messages = append(messages, err.Error())
			fmt.Fprintf(r.Command.ErrOrStderr(), "%s failed (%s)\n", name, err)
		} else {
			fmt.Fprintf(r.Command.OutOrStdout(), "%s no longer proposed for %s\n", name, proposedFor)
		}
	}
	if len(messages) > 0 {
		return errors.E(op, fmt.Errorf("errors:\n  %s", strings.Join(messages, "\n  ")))
	}

	return nil
}
