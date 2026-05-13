// Copyright 2022-2026 The kpt Authors
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

package reg

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/repo/docs"
	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	command = "cmdreporeg"
)

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		ctx: ctx,
		cfg: rcg,
	}
	c := &cobra.Command{
		Use:     "reg REPOSITORY",
		Aliases: []string{"register"},
		Short:   docs.RegShort,
		Long:    docs.RegShort + "\n" + docs.RegLong,
		Example: docs.RegExamples,
		PreRunE: r.preRunE,
		RunE:    r.runE,
		Hidden:  cliutils.HidePorchCommands,
	}
	r.Command = c

	c.Flags().StringVar(&r.directory, "directory", "/", "Directory within the repository where to look for packages.")
	c.Flags().StringVar(&r.branch, "branch", "main", "Branch in the repository where finalized packages are committed.")
	c.Flags().BoolVar(&r.createBranch, "create-branch", false, "Create the package branch if it doesn't already exist.")
	c.Flags().StringVar(&r.name, "name", "", "Name of the package repository. If unspecified, will use the name portion (last segment) of the repository URL.")
	c.Flags().StringVar(&r.description, "description", "", "Brief description of the package repository.")
	c.Flags().BoolVar(&r.deployment, "deployment", false, "Repository is a deployment repository; packages in a deployment repository are considered deployment-ready.")
	c.Flags().StringVar(&r.username, "repo-basic-username", "", "Username for repository authentication using basic auth.")
	c.Flags().StringVar(&r.password, "repo-basic-password", "", "Password for repository authentication using basic auth.")
	c.Flags().BoolVar(&r.workloadIdentity, "repo-workload-identity", false, "Use workload identity for authentication with the repo")
	c.Flags().StringVar(&r.syncSchedule, "sync-schedule", "", "Cron schedule for reconciling packages in the repository.")
	c.Flags().BoolVar(&r.v1alpha2, "v1alpha2", false, "Enable v1alpha2 PackageRevision management for this repository.")

	return r
}

func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	return newRunner(ctx, rcg).Command
}

type runner struct {
	ctx     context.Context
	cfg     *genericclioptions.ConfigFlags
	client  client.Client
	Command *cobra.Command

	// Flags
	directory        string
	branch           string
	createBranch     bool
	name             string
	description      string
	deployment       bool
	username         string
	password         string
	workloadIdentity bool
	syncSchedule     string
	v1alpha2         bool
}

func (r *runner) preRunE(_ *cobra.Command, _ []string) error {
	const op errors.Op = command + ".preRunE"

	// todo if namespace flag missing, use kubeconfig
	if *r.cfg.Namespace == "" {
		// Get the namespace from kubeconfig
		namespace, _, err := r.cfg.ToRawKubeConfigLoader().Namespace()
		if err != nil {
			return fmt.Errorf("error getting namespace: %w", err)
		}
		r.cfg.Namespace = &namespace
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

	if len(args) == 0 {
		return errors.E(op, "repository is required positional argument")
	}

	repository := args[0]

	var git *configapi.GitRepository
	var oci *configapi.OciRepository
	var rt configapi.RepositoryType

	if strings.HasPrefix(repository, "oci://") {
		rt = configapi.RepositoryTypeOCI
		oci = &configapi.OciRepository{
			Registry: repository[6:],
		}
		if r.name == "" {
			r.name = cliutils.LastSegment(repository)
		}
	} else {
		rt = configapi.RepositoryTypeGit
		// TODO: better parsing.
		// t, err := parse.GitParseArgs(r.ctx, []string{repository, "."})
		// if err != nil {
		// 	return errors.E(op, err)
		// }
		git = &configapi.GitRepository{
			Repo:         repository,
			Branch:       r.branch,
			CreateBranch: r.createBranch,
			Directory:    r.directory,
		}

		if r.name == "" {
			r.name = cliutils.LastSegment(repository)
		}
	}

	secret, err := r.buildAuthSecret()
	if err != nil {
		return err
	}
	if secret != nil {
		if err := r.client.Create(r.ctx, secret); err != nil {
			return errors.E(op, err)
		}

		if git != nil {
			git.SecretRef.Name = secret.Name
		}
		if oci != nil {
			oci.SecretRef.Name = secret.Name
		}
	}

	var sync *configapi.RepositorySync
	if r.syncSchedule != "" {
		// validate the cron expression
		if err := validateCronExpression(r.syncSchedule); err != nil {
			return errors.E(op, fmt.Errorf("invalid sync-schedule cron expression: %w", err))
		}
		sync = &configapi.RepositorySync{
			Schedule: r.syncSchedule,
		}
	}

	annotations := map[string]string{}
	if r.v1alpha2 {
		annotations[configapi.AnnotationKeyV1Alpha2Migration] = configapi.AnnotationValueMigrationEnabled
	}

	if err := r.client.Create(r.ctx, &configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Repository",
			APIVersion: configapi.GroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.name,
			Namespace:   *r.cfg.Namespace,
			Annotations: annotations,
		},
		Spec: configapi.RepositorySpec{
			Description: r.description,
			Type:        rt,
			Deployment:  r.deployment,
			Git:         git,
			Oci:         oci,
			Sync:        sync,
		},
	}); err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (r *runner) buildAuthSecret() (*coreapi.Secret, error) {
	var basicAuth bool
	var workloadIdentity bool

	if r.username != "" || r.password != "" {
		basicAuth = true
	}

	workloadIdentity = r.workloadIdentity

	if workloadIdentity && basicAuth {
		return nil, fmt.Errorf("both username/password and workload identity specified")
	}

	switch {
	case workloadIdentity:
		return &coreapi.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: coreapi.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-auth", r.name),
				Namespace: *r.cfg.Namespace,
			},
			Data: map[string][]byte{},
			Type: "kpt.dev/workload-identity-auth",
		}, nil
	case basicAuth:
		return &coreapi.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: coreapi.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-auth", r.name),
				Namespace: *r.cfg.Namespace,
			},
			Data: map[string][]byte{
				"username": []byte(r.username),
				"password": []byte(r.password),
			},
			Type: coreapi.SecretTypeBasicAuth,
		}, nil
	}
	return nil, nil
}

func validateCronExpression(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}
