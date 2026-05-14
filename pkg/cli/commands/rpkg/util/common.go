// Copyright 2023 The kpt Authors
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

package util

import (
	"context"
	"fmt"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/errors"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ResourceVersionAnnotation = "internal.kpt.dev/resource-version"
)

// InitClient creates a controller-runtime client and validates the namespace flag.
// If --namespace is specified without a value, it returns an error immediately.
// A nil cfg is rejected up front so callers (and tests) cannot trigger a panic
// inside cliutils.CreateClientWithFlags -> ConfigFlags.ToRESTConfig.
func InitClient(cmd *cobra.Command, cfg *genericclioptions.ConfigFlags) (client.Client, error) {
	// pflag stores the -n shorthand against the same flag entry as the
	// --namespace long form, so a single lookup of "namespace" covers both.
	nsFlag := cmd.Flag("namespace")
	if nsFlag != nil && nsFlag.Changed && nsFlag.Value.String() == "" {
		return nil, fmt.Errorf("namespace flag specified without a value; please provide a value for --namespace/-n or omit the flag")
	}

	if cfg == nil {
		return nil, fmt.Errorf("cfg must not be nil")
	}

	c, err := cliutils.CreateClientWithFlags(cfg)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// CreateScheme returns a runtime.Scheme registered with the type sets that
// rpkg subcommands operate on: porchapi (PackageRevision and friends), the
// porchconfig API (Repository), corev1 (ConfigMap and other core kinds), and
// metav1 (Status, WatchEvent and other meta types required for list/watch
// decoding). The set mirrors the scheme used by cliutils.CreateClientWithFlags
// so that commands which build their own client see the same kinds.
func CreateScheme() (*runtime.Scheme, error) {
	return buildScheme([]func(*runtime.Scheme) error{
		porchapi.AddToScheme,
		configapi.AddToScheme,
		corev1.AddToScheme,
		metav1.AddMetaToScheme,
	})
}

// buildScheme registers the given AddToScheme functions on a fresh scheme,
// returning the first error encountered. Splitting this out lets tests inject
// a deliberately failing adder to cover the error branch.
func buildScheme(adders []func(*runtime.Scheme) error) (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, add := range adders {
		if err := add(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}

// MakePreRunE returns a cobra PreRunE function that validates the namespace flag
// and creates a controller-runtime client, storing it in *clientPtr.
// Pass the command's op constant for error context (e.g. command + ".preRunE").
func MakePreRunE(op errors.Op, cfg *genericclioptions.ConfigFlags, clientPtr *client.Client) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		c, err := InitClient(cmd, cfg)
		if err != nil {
			return errors.E(op, err)
		}
		*clientPtr = c
		return nil
	}
}

// Runner holds the common fields used by rpkg lifecycle commands.
//
// Ctx is intentionally stored on the struct rather than passed per-method.
// Each rpkg subcommand is a short-lived CLI runner whose lifetime equals one
// command invocation; the context is captured at construction time from the
// outer cobra command and used by the embedded methods (preRunE/runE) that
// cobra invokes through a fixed signature. This mirrors the per-command
// `runner` struct convention that already exists across rpkg subpackages.
// See SonarQube rule godre:S8242 for the general guidance against contexts
// in structs; this exception is documented rather than refactored to avoid
// fanning a behavioural change out across every rpkg subcommand.
type Runner struct {
	Ctx     context.Context
	Cfg     *genericclioptions.ConfigFlags
	Client  client.Client
	Command *cobra.Command
}

// NewTestRunner returns a Runner pre-wired for table-driven CLI tests in the
// rpkg sub-packages. It accepts a namespace string (heap-escaped via &ns),
// a fake client, and the cobra command under test.
func NewTestRunner(ns string, c client.Client, cmd *cobra.Command) Runner {
	return Runner{
		Ctx:     context.Background(),
		Cfg:     &genericclioptions.ConfigFlags{Namespace: &ns},
		Client:  c,
		Command: cmd,
	}
}

// retryOnConflict retries fn on conflict errors with exponential backoff.
// Unlike retry.RetryOnConflict from client-go, this wrapper tracks the last
// error from fn and returns it reliably even when the underlying library
// silently drops it due to a known edge case in ExponentialBackoff.
func retryOnConflict(fn func() error) error {
	var lastErr error
	wrapped := func() error {
		lastErr = fn()
		return lastErr
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, wrapped)
	if err == nil && lastErr != nil {
		return lastErr
	}
	return err
}

// PackageAction processes a single PackageRevision and returns a success message.
// Return ("", nil) to skip the standard success print. Callbacks that have
// already written their own informational output to the command's output stream
// take this path, for example propose and propose-delete printing
// "already proposed" before returning.
type PackageAction func(ctx context.Context, client client.Client, pr *porchapi.PackageRevision) (string, error)

// RunForEachOpts groups the behavioural flags accepted by RunForEachPackage so
// that call sites read as `WithRetry: true, CheckReadiness: true` instead of
// two anonymous booleans.
//
// CmdName is the per-subcommand identifier (e.g. "cmdrpkgapprove") used to
// build the errors.Op tag suffixed with ".runE". It is required; passing an
// empty string is rejected up front by RunForEachPackage rather than producing
// a malformed ".runE" op tag.
type RunForEachOpts struct {
	CmdName        string
	WithRetry      bool
	CheckReadiness bool
}

// fetchAndAct fetches the named PackageRevision, optionally verifies readiness,
// then dispatches to action. Splitting it out keeps RunForEachPackage's
// per-iteration body shallow.
func fetchAndAct(
	ctx context.Context,
	c client.Client,
	key client.ObjectKey,
	checkReadiness bool,
	action PackageAction,
) (string, error) {
	var pr porchapi.PackageRevision
	if err := c.Get(ctx, key, &pr); err != nil {
		return "", err
	}
	if checkReadiness && !porchapi.PackageRevisionIsReady(pr.Spec.ReadinessGates, pr.Status.Conditions) {
		return "", fmt.Errorf("readiness conditions not met")
	}
	return action(ctx, c, &pr)
}

// reportResult prints the per-package outcome and appends the error (if any)
// to messages. Centralising the print/append logic keeps the caller free of
// the if/else-if branching that previously inflated cognitive complexity.
func reportResult(cmd *cobra.Command, name, successMsg string, err error, messages *[]string) {
	if err != nil {
		*messages = append(*messages, err.Error())
		fmt.Fprintf(cmd.ErrOrStderr(), "%s failed (%s)\n", name, err)
		return
	}
	if successMsg != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", successMsg)
	}
}

// RunForEachPackage loops over package names, fetches each PackageRevision,
// optionally runs a readiness pre-check, then runs the action. Errors are
// collected and reported. When opts.WithRetry is true, each iteration is
// retried on conflict. When opts.CheckReadiness is true, readiness gates
// are verified before the action runs.
func RunForEachPackage(
	ctx context.Context,
	c client.Client,
	cmd *cobra.Command,
	namespace string,
	args []string,
	opts RunForEachOpts,
	action PackageAction,
) error {
	if opts.CmdName == "" {
		return fmt.Errorf("RunForEachOpts.CmdName must not be empty")
	}
	op := errors.Op(opts.CmdName + ".runE")
	if len(args) == 0 {
		return errors.E(op, fmt.Errorf("PACKAGE is a required positional argument"))
	}
	var messages []string

	for _, name := range args {
		key := client.ObjectKey{Namespace: namespace, Name: name}
		var successMsg string
		run := func() error {
			msg, err := fetchAndAct(ctx, c, key, opts.CheckReadiness, action)
			successMsg = msg
			return err
		}

		var err error
		if opts.WithRetry {
			err = retryOnConflict(run)
		} else {
			err = run()
		}
		reportResult(cmd, name, successMsg, err, &messages)
	}

	if len(messages) > 0 {
		return errors.E(op, fmt.Errorf("errors:\n  %s", strings.Join(messages, "\n  ")))
	}
	return nil
}

func GetResourceFileKubeObject(prr *porchapi.PackageRevisionResources, file, kind, name string) (*fnsdk.KubeObject, error) {
	if prr.Spec.Resources == nil {
		return nil, fmt.Errorf("nil resources found for PackageRevisionResources '%s/%s'", prr.Namespace, prr.Name)
	}

	if _, ok := prr.Spec.Resources[file]; !ok {
		return nil, fmt.Errorf("%q not found in PackageRevisionResources '%s/%s'", file, prr.Namespace, prr.Name)
	}

	ko, err := fnsdk.ParseKubeObject([]byte(prr.Spec.Resources[file]))
	if err != nil {
		return nil, fmt.Errorf("failed to parse %q of PackageRevisionResources %s/%s: %w", file, prr.Namespace, prr.Name, err)
	}
	if kind != "" && ko.GetKind() != kind {
		return nil, fmt.Errorf("%q does not contain kind %q in PackageRevisionResources '%s/%s'", file, kind, prr.Namespace, prr.Name)
	}
	if name != "" && ko.GetName() != name {
		return nil, fmt.Errorf("%q does not contain resource named %q in PackageRevisionResources '%s/%s'", file, name, prr.Namespace, prr.Name)
	}

	return ko, nil
}

func GetResourceVersion(prr *porchapi.PackageRevisionResources) (string, error) {
	ko, err := GetResourceFileKubeObject(prr, kptfilev1.RevisionMetaDataFileName, kptfilev1.RevisionMetaDataKind, "")
	if err != nil {
		return "", err
	}
	rv, _, _ := ko.NestedString("metadata", "resourceVersion")
	return rv, nil
}

func AddRevisionMetadata(prr *porchapi.PackageRevisionResources) error {
	kptMetaDataKo := fnsdk.NewEmptyKubeObject()
	if err := kptMetaDataKo.SetAPIVersion(prr.APIVersion); err != nil {
		return fmt.Errorf("cannot set Api Version: %v", err)
	}
	if err := kptMetaDataKo.SetKind(kptfilev1.RevisionMetaDataKind); err != nil {
		return fmt.Errorf("cannot set Kind: %v", err)
	}
	if err := kptMetaDataKo.SetNestedField(prr.GetObjectMeta(), "metadata"); err != nil {
		return fmt.Errorf("cannot set metadata: %v", err)
	}
	prr.Spec.Resources[kptfilev1.RevisionMetaDataFileName] = kptMetaDataKo.String()

	return nil
}

func RemoveRevisionMetadata(prr *porchapi.PackageRevisionResources) error {
	delete(prr.Spec.Resources, kptfilev1.RevisionMetaDataFileName)
	return nil
}
