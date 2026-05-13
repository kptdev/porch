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

package porch

import (
	"fmt"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// makeTree creates a parent command with --api-version persistent flag
// and a child command, mimicking the real rpkg command tree.
func makeTree(childRunE func(*cobra.Command, []string) error) (*cobra.Command, *cobra.Command) {
	parent := &cobra.Command{Use: "root"}
	parent.PersistentFlags().String(FlagAPIVersion, "", "")
	child := &cobra.Command{
		Use:     "test",
		PreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		RunE:    childRunE,
	}
	parent.AddCommand(child)
	return parent, child
}

func TestGetAPIVersion_Default(t *testing.T) {
	os.Unsetenv(EnvAPIVersion)
	parent, child := makeTree(func(cmd *cobra.Command, _ []string) error {
		if got := GetAPIVersion(cmd); got != APIVersionV1Alpha1 {
			return fmt.Errorf("expected %s, got %s", APIVersionV1Alpha1, got)
		}
		return nil
	})
	parent.SetArgs([]string{"test"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	// Also test directly on child (pre-Execute)
	if got := GetAPIVersion(child); got != APIVersionV1Alpha1 {
		t.Errorf("expected %s, got %s", APIVersionV1Alpha1, got)
	}
}

func TestGetAPIVersion_Flag(t *testing.T) {
	os.Unsetenv(EnvAPIVersion)
	parent, _ := makeTree(func(cmd *cobra.Command, _ []string) error {
		if got := GetAPIVersion(cmd); got != APIVersionV1Alpha2 {
			return fmt.Errorf("expected %s, got %s", APIVersionV1Alpha2, got)
		}
		return nil
	})
	parent.SetArgs([]string{"test", "--api-version=v1alpha2"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestGetAPIVersion_Env(t *testing.T) {
	os.Setenv(EnvAPIVersion, APIVersionV1Alpha2)
	defer os.Unsetenv(EnvAPIVersion)
	parent, _ := makeTree(func(cmd *cobra.Command, _ []string) error {
		if got := GetAPIVersion(cmd); got != APIVersionV1Alpha2 {
			return fmt.Errorf("expected %s, got %s", APIVersionV1Alpha2, got)
		}
		return nil
	})
	parent.SetArgs([]string{"test"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestGetAPIVersion_FlagOverridesEnv(t *testing.T) {
	os.Setenv(EnvAPIVersion, APIVersionV1Alpha2)
	defer os.Unsetenv(EnvAPIVersion)
	parent, _ := makeTree(func(cmd *cobra.Command, _ []string) error {
		if got := GetAPIVersion(cmd); got != APIVersionV1Alpha1 {
			return fmt.Errorf("expected %s, got %s", APIVersionV1Alpha1, got)
		}
		return nil
	})
	parent.SetArgs([]string{"test", "--api-version=v1alpha1"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestIsV1Alpha2(t *testing.T) {
	os.Unsetenv(EnvAPIVersion)
	var gotDefault, gotV2 bool
	parent, _ := makeTree(func(cmd *cobra.Command, _ []string) error {
		gotDefault = IsV1Alpha2(cmd)
		return nil
	})
	parent.SetArgs([]string{"test"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotDefault {
		t.Error("expected false for default")
	}

	parent2, _ := makeTree(func(cmd *cobra.Command, _ []string) error {
		gotV2 = IsV1Alpha2(cmd)
		return nil
	})
	parent2.SetArgs([]string{"test", "--api-version=v1alpha2"})
	if err := parent2.Execute(); err != nil {
		t.Fatal(err)
	}
	if !gotV2 {
		t.Error("expected true for v1alpha2")
	}
}

func TestWrapVersionDispatch(t *testing.T) {
	var v1Called, v2Called bool

	parent := &cobra.Command{Use: "root"}
	parent.PersistentFlags().String(FlagAPIVersion, "", "")

	child := &cobra.Command{
		Use:     "test",
		PreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		RunE: func(_ *cobra.Command, _ []string) error {
			v1Called = true
			return nil
		},
	}
	parent.AddCommand(child)

	WrapVersionDispatch(child,
		func(_ *cobra.Command, _ []string) error { return nil },
		func(_ *cobra.Command, _ []string) error {
			v2Called = true
			return nil
		},
	)

	// Default: v1alpha1
	v1Called, v2Called = false, false
	parent.SetArgs([]string{"test"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	if !v1Called || v2Called {
		t.Errorf("expected v1 called, got v1=%v v2=%v", v1Called, v2Called)
	}

	// With flag: v1alpha2
	v1Called, v2Called = false, false
	parent.SetArgs([]string{"test", "--api-version=v1alpha2"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	if v1Called || !v2Called {
		t.Errorf("expected v2 called, got v1=%v v2=%v", v1Called, v2Called)
	}
}

func TestWrapVersionDispatch_V2PreRunEError(t *testing.T) {
	parent := &cobra.Command{Use: "root"}
	parent.PersistentFlags().String(FlagAPIVersion, "", "")

	child := &cobra.Command{
		Use:     "test",
		PreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		RunE:    func(_ *cobra.Command, _ []string) error { return nil },
	}
	parent.AddCommand(child)

	WrapVersionDispatch(child,
		func(_ *cobra.Command, _ []string) error { return fmt.Errorf("v2 prerun error") },
		func(_ *cobra.Command, _ []string) error { return nil },
	)

	parent.SetArgs([]string{"test", "--api-version=v1alpha2"})
	err := parent.Execute()
	if err == nil {
		t.Fatal("expected error from v2 preRunE")
	}
}

func TestWrapVersionDispatch_NilV1PreRunE(t *testing.T) {
	parent := &cobra.Command{Use: "root"}
	parent.PersistentFlags().String(FlagAPIVersion, "", "")

	child := &cobra.Command{
		Use:  "test",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
		// No PreRunE set
	}
	parent.AddCommand(child)

	WrapVersionDispatch(child, nil,
		func(_ *cobra.Command, _ []string) error { return nil },
	)

	// Should not panic when v1 PreRunE is nil
	parent.SetArgs([]string{"test"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
}
