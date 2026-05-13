// Copyright 2025-2026 The kpt Authors
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

package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/kptdev/kpt/pkg/lib/errors"
	"github.com/kptdev/kpt/pkg/lib/util/cmdutil"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/repo/docs"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/cmd/get"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	command = "cmdreposync"
)

func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	return newRunner(ctx, rcg).Command
}

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		ctx:        ctx,
		getFlags:   cmdutil.Options{ConfigFlags: rcg},
		printFlags: get.NewGetPrintFlags(),
	}
	c := &cobra.Command{
		Use:     "sync [REPOSITORY_NAME]",
		Aliases: []string{"reconcile"},
		Short:   docs.SyncShort,
		Long:    docs.SyncShort + "\n" + docs.SyncLong,
		Example: docs.SyncExamples,
		PreRunE: r.preRunE,
		RunE:    r.runE,
		Hidden:  cliutils.HidePorchCommands,
	}
	r.Command = c

	r.getFlags.AddFlags(c)
	r.printFlags.AddFlags(c)

	c.Flags().Bool("all", false, "Reconcile all repositories in the namespace")
	c.Flags().String("run-once", "", "Schedule a one-time sync using a duration (e.g., 10m, 2h30m) or RFC3339 timestamp (e.g., 2025-09-16T12:00:00Z). Minimum allowed duration and default value is 1 minute from time of command invocation.")

	return r
}

type runner struct {
	ctx        context.Context
	Command    *cobra.Command
	getFlags   cmdutil.Options
	printFlags *get.PrintFlags
	client     client.Client
}

func (r *runner) preRunE(cmd *cobra.Command, _ []string) error {
	r.printFlags.HumanReadableFlags.WithNamespace = cmd.Flags().Lookup("all-namespaces").Value.String() == "true"
	return nil
}

func (r *runner) runE(cmd *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"

	var k8sClient client.Client
	var err error

	if r.client != nil {
		k8sClient = r.client
	} else {
		k8sClient, err = cliutils.CreateClientWithFlags(r.getFlags.ConfigFlags)
		if err != nil {
			return errors.E(op, err)
		}
	}
	allFlag := cmd.Flags().Lookup("all").Value.String() == "true"
	allNamespaces := cmd.Flags().Lookup("all-namespaces").Value.String() == "true"

	namespace := ""
	if r.getFlags.Namespace == nil || *r.getFlags.Namespace == "" {
		if !allNamespaces {
			return fmt.Errorf("namespace must be specified unless --all-namespaces is set")
		}
	} else {
		namespace = *r.getFlags.Namespace
	}

	runOnceStr := cmd.Flags().Lookup("run-once").Value.String()
	var runOnceAtTime time.Time
	minDelay := 1 * time.Minute

	if runOnceStr != "" {
		if duration, err := time.ParseDuration(runOnceStr); err == nil {
			runOnceAtTime = time.Now().Add(duration)
			if duration < minDelay {
				runOnceAtTime = time.Now().Add(minDelay)
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: Duration must be at least 1 minute. Sync time adjusted to comply with minimum delay.\n")
			}
		} else if timestamp, err := time.Parse(time.RFC3339, runOnceStr); err == nil {
			delay := time.Until(timestamp)
			if delay < minDelay {
				runOnceAtTime = time.Now().Add(minDelay)
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: Scheduled time is within 1 minute or in the past. Sync time adjusted to comply with minimum delay.\n")
			} else {
				runOnceAtTime = timestamp
			}
		} else {
			return fmt.Errorf("invalid --run-once value")
		}
	} else {
		runOnceAtTime = time.Now().Add(minDelay)
	}

	var repos []configapi.Repository

	if allFlag {
		list := &configapi.RepositoryList{}
		listOpts := client.ListOptions{}
		if !allNamespaces {
			listOpts.Namespace = namespace
		}
		if err := k8sClient.List(r.ctx, list, &listOpts); err != nil {
			return errors.E(op, err)
		}
		repos = list.Items
	} else {
		if len(args) == 0 {
			return fmt.Errorf("repository name(s) required unless --all is set")
		}
		for _, repoName := range args {
			repo := &configapi.Repository{}
			if err := k8sClient.Get(r.ctx, types.NamespacedName{Namespace: namespace, Name: repoName}, repo); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Error fetching repository %q: %v\n", repoName, err)
				continue
			}
			repos = append(repos, *repo)
		}
	}

	if len(repos) == 0 {
		if allNamespaces {
			return fmt.Errorf("no repositories found across all namespaces")
		}
		return fmt.Errorf("no repositories found in namespace %q", namespace)
	}

	for _, repo := range repos {
		if repo.Spec.Sync == nil {
			repo.Spec.Sync = &configapi.RepositorySync{}
		}
		repo.Spec.Sync.RunOnceAt = &metav1.Time{Time: runOnceAtTime}

		if err := k8sClient.Update(r.ctx, &repo); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to update repository %q: %v\n", repo.Name, err)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Scheduled one-time sync for %q at %s\n", repo.Name, runOnceAtTime.Format(time.RFC3339))
	}

	return nil
}
