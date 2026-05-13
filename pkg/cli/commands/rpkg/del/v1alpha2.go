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

package del

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type v1alpha2Runner struct {
	ctx    context.Context
	cfg    *genericclioptions.ConfigFlags
	client client.Client
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
	var messages []string

	for _, pkg := range args {
		pr := &porchv1alpha2.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: *r.cfg.Namespace,
				Name:      pkg,
			},
		}
		if err := r.client.Delete(r.ctx, pr); err != nil {
			messages = append(messages, err.Error())
			fmt.Fprintf(cmd.ErrOrStderr(), "%s failed (%s)\n", pkg, err)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s deleted\n", pkg)
		}
	}
	if len(messages) > 0 {
		return errors.E(op, fmt.Errorf("errors:\n  %s", strings.Join(messages, "\n  ")))
	}
	return nil
}
