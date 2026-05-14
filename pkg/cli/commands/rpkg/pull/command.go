// Copyright 2022 The kpt Authors
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

package pull

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/errors"
	"github.com/kptdev/kpt/pkg/lib/util/cmdutil"
	"github.com/kptdev/kpt/pkg/printer"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	rpkgutil "github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/kio/kioutil"
)

const (
	command = "cmdrpkgpull"
)

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		Runner: rpkgutil.Runner{Ctx: ctx, Cfg: rcg},
	}
	c := &cobra.Command{
		Use:        "pull PACKAGE [DIR]",
		Aliases:    []string{"source", "read"},
		SuggestFor: []string{},
		Short:      docs.PullShort,
		Long:       docs.PullShort + "\n" + docs.PullLong,
		Example:    docs.PullExamples,
		PreRunE:    r.preRunE,
		RunE:       r.runE,
		Hidden:     cliutils.HidePorchCommands,
	}
	r.Command = c
	return r
}

// NewCommand returns the cobra command for `rpkg pull`, which fetches
// the resources of a package revision and writes them to a local
// directory or stdout for inspection or editing.
func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	return newRunner(ctx, rcg).Command
}

type runner struct {
	rpkgutil.Runner
	printer printer.Printer
}

func (r *runner) preRunE(_ *cobra.Command, _ []string) error {
	const op errors.Op = command + ".preRunE"
	config, err := r.Cfg.ToRESTConfig()
	if err != nil {
		return errors.E(op, err)
	}

	scheme, err := rpkgutil.CreateScheme()
	if err != nil {
		return errors.E(op, err)
	}

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return errors.E(op, err)
	}

	r.Client = c
	r.printer = printer.FromContextOrDie(r.Ctx)
	return nil
}

func (r *runner) runE(_ *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"

	if len(args) == 0 {
		return errors.E(op, "PACKAGE is a required positional argument")
	}

	packageName := args[0]

	var resources porchapi.PackageRevisionResources
	if err := r.Client.Get(r.Ctx, client.ObjectKey{
		Namespace: *r.Cfg.Namespace,
		Name:      packageName,
	}, &resources); err != nil {
		return errors.E(op, err)
	}

	if err := rpkgutil.AddRevisionMetadata(&resources); err != nil {
		return errors.E(op, err)
	}

	if len(args) > 1 {
		if err := writeToDir(resources.Spec.Resources, args[1]); err != nil {
			return errors.E(op, err)
		}
	} else {
		if err := writeToWriter(resources.Spec.Resources, r.printer.OutStream()); err != nil {
			return errors.E(op, err)
		}
	}
	return nil
}

func writeToDir(resources map[string]string, dir string) error {
	if err := cmdutil.CheckDirectoryNotPresent(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	for k, v := range resources {
		f := filepath.Join(dir, k)
		d := filepath.Dir(f)
		if err := os.MkdirAll(d, 0750); err != nil {
			return err
		}
		if err := os.WriteFile(f, []byte(v), 0600); err != nil {
			return err
		}
	}
	return nil
}

func writeToWriter(resources map[string]string, out io.Writer) error {
	keys := make([]string, 0, len(resources))
	for k := range resources {
		if !includeFile(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Create kio readers
	inputs := []kio.Reader{}
	for _, k := range keys {
		v := resources[k]
		inputs = append(inputs, &kio.ByteReader{
			Reader: strings.NewReader(v),
			SetAnnotations: map[string]string{
				kioutil.PathAnnotation: k,
			},
			DisableUnwrapping: true,
		})
	}

	return kio.Pipeline{
		Inputs: inputs,
		Outputs: []kio.Writer{
			kio.ByteWriter{
				Writer:                out,
				KeepReaderAnnotations: true,
				WrappingKind:          kio.ResourceListKind,
				WrappingAPIVersion:    kio.ResourceListAPIVersion,
				Sort:                  true,
			},
		},
	}.Execute()
}

var matchResourceContents = append(kio.MatchAll, kptfilev1.KptFileName, kptfilev1.RevisionMetaDataFileName)

func includeFile(path string) bool {
	for _, m := range matchResourceContents {
		// Only use the filename for the check for whether we should
		// include the file.
		f := filepath.Base(path)
		if matched, err := filepath.Match(m, f); err == nil && matched {
			return true
		}
	}
	return false
}
