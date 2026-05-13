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

package proposedelete

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newV1Alpha2Runner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *v1alpha2Runner {
	r := &v1alpha2Runner{ctx: ctx, cfg: rcg}
	r.Command = &cobra.Command{
		Use:        "propose-delete PACKAGE",
		Aliases:    []string{"propose-del"},
		Short:      docs.ProposeDeleteShort,
		Long:       docs.ProposeDeleteShort + "\n" + docs.ProposeDeleteLong,
		Example:    docs.ProposeDeleteExamples,
		SuggestFor: []string{},
		PreRunE:    r.preRunE,
		RunE:       r.runE,
		Hidden:     cliutils.HidePorchCommands,
	}
	return r
}

type v1alpha2Runner struct {
	ctx     context.Context
	cfg     *genericclioptions.ConfigFlags
	client  client.Client
	Command *cobra.Command
}

func (r *v1alpha2Runner) preRunE(_ *cobra.Command, _ []string) error {
	const op errors.Op = command + ".preRunE"
	if r.client == nil {
		c, err := cliutils.CreateV1Alpha2ClientWithFlags(r.cfg)
		if err != nil {
			return errors.E(op, err)
		}
		r.client = c
	}
	return nil
}

func (r *v1alpha2Runner) runE(_ *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"
	var messages []string
	namespace := *r.cfg.Namespace

	for _, name := range args {
		key := client.ObjectKey{Namespace: namespace, Name: name}
		var lastErr error
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var pr porchv1alpha2.PackageRevision
			if err := r.client.Get(r.ctx, key, &pr); err != nil {
				lastErr = err
				return err
			}
			switch pr.Spec.Lifecycle {
			case porchv1alpha2.PackageRevisionLifecyclePublished:
				pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
				err := r.client.Update(r.ctx, &pr)
				if err == nil {
					lastErr = nil
					fmt.Fprintf(r.Command.OutOrStdout(), "%s proposed for deletion\n", name)
				} else {
					lastErr = err
				}
				return err
			case porchv1alpha2.PackageRevisionLifecycleDeletionProposed:
				lastErr = nil
				fmt.Fprintf(r.Command.OutOrStderr(), "%s is already proposed for deletion\n", name)
				return nil
			default:
				lastErr = fmt.Errorf("can only propose published packages for deletion; package %s is not published", name)
				return lastErr
			}
		})
		if err == nil && lastErr != nil {
			err = lastErr
		}
		if err != nil {
			messages = append(messages, err.Error())
			fmt.Fprintf(r.Command.ErrOrStderr(), "%s failed (%s)\n", name, err)
		}
	}
	if len(messages) > 0 {
		return errors.E(op, fmt.Errorf("errors:\n  %s", strings.Join(messages, "\n  ")))
	}
	return nil
}
