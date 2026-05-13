// Copyright 2022, 2024, 2026 The kpt Authors
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

package packagerevision

// This file is a local copy of merge-key utilities from pkg/task/mergekey.go
// and pkg/task/kio.go. Duplicated here to avoid importing pkg/task which
// transitively pulls in v1alpha1 types.

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/util/addmergecomment"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/kio/kioutil"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// ensureMergeKey adds merge-key comment directives to resources so that
// package update can reconcile resource identity between upstream and downstream.
func ensureMergeKey(resources map[string]string) (map[string]string, error) {
	pr := &mapReader{
		input: resources,
		extra: map[string]string{},
	}

	result := map[string]string{}

	pipeline := kio.Pipeline{
		Inputs:  []kio.Reader{pr},
		Filters: []kio.Filter{kio.FilterAll(&addmergecomment.AddMergeComment{})},
		Outputs: []kio.Writer{&mapWriter{output: result}},
	}

	if err := pipeline.Execute(); err != nil {
		return nil, fmt.Errorf("failed to add merge-key directive: %w", err)
	}

	for k, v := range pr.extra {
		result[k] = v
	}

	return result, nil
}

type mapReader struct {
	input map[string]string
	extra map[string]string
}

func (r *mapReader) Read() ([]*yaml.RNode, error) {
	var results []*yaml.RNode
	for k, v := range r.input {
		base := path.Base(k)
		ext := path.Ext(base)

		if ext != ".yaml" && ext != ".yml" && base != "Kptfile" {
			r.extra[k] = v
			continue
		}

		nodes, err := (&kio.ByteReader{
			Reader:            strings.NewReader(v),
			SetAnnotations:    map[string]string{kioutil.PathAnnotation: k},
			DisableUnwrapping: true,
		}).Read()
		if err != nil {
			return nil, err
		}
		results = append(results, nodes...)
	}
	return results, nil
}

type mapWriter struct {
	output map[string]string
}

func (w *mapWriter) Write(nodes []*yaml.RNode) error {
	paths := map[string][]*yaml.RNode{}
	for _, node := range nodes {
		p := nodePathAnnotation(node)
		paths[p] = append(paths[p], node)
	}

	buf := &bytes.Buffer{}
	for p, nodes := range paths {
		bw := kio.ByteWriter{
			Writer: buf,
			ClearAnnotations: []string{
				kioutil.PathAnnotation,
				//nolint:staticcheck
				kioutil.LegacyPathAnnotation,
			},
		}
		if err := bw.Write(nodes); err != nil {
			return err
		}
		w.output[p] = buf.String()
		buf.Reset()
	}
	return nil
}

func nodePathAnnotation(node *yaml.RNode) string {
	ann := node.GetAnnotations()
	if p, ok := ann[kioutil.PathAnnotation]; ok {
		return p
	}
	ns := node.GetNamespace()
	if ns == "" {
		ns = "non-namespaced"
	}
	name := node.GetName()
	if name == "" {
		name = "unnamed"
	}
	return path.Join(ns, fmt.Sprintf("%s.yaml", name))
}
