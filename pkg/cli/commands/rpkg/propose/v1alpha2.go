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

package propose

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type v1alpha2Runner struct {
	ctx    context.Context
	cfg    *genericclioptions.ConfigFlags
	client client.Client
	cmd    *cobra.Command
}

func newV1Alpha2Runner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *v1alpha2Runner {
	return &v1alpha2Runner{ctx: ctx, cfg: rcg}
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

func (r *v1alpha2Runner) runE(cmd *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"
	r.cmd = cmd
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
			if !porchv1alpha2.PackageRevisionIsReady(pr.Spec.ReadinessGates, pr.Status.PackageConditions) {
				lastErr = fmt.Errorf("readiness conditions not met")
				return lastErr
			}
			switch pr.Spec.Lifecycle {
			case porchv1alpha2.PackageRevisionLifecycleDraft:
				pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
				err := r.client.Update(r.ctx, &pr)
				if err == nil {
					lastErr = nil
					fmt.Fprintf(cmd.OutOrStdout(), "%s proposed\n", name)
				} else {
					lastErr = err
				}
				return err
			case porchv1alpha2.PackageRevisionLifecycleProposed:
				lastErr = nil
				fmt.Fprintf(cmd.OutOrStderr(), "%s is already proposed\n", name)
				return nil
			default:
				lastErr = fmt.Errorf("cannot propose %s package", pr.Spec.Lifecycle)
				return lastErr
			}
		})
		if err == nil && lastErr != nil {
			err = lastErr
		}
		if err != nil {
			messages = append(messages, err.Error())
			fmt.Fprintf(cmd.ErrOrStderr(), "%s failed (%s)\n", name, err)
		}
	}
	if len(messages) > 0 {
		return errors.E(op, fmt.Errorf("errors:\n  %s", strings.Join(messages, "\n  ")))
	}
	return nil
}
