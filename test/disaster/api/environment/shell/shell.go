// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package shell

import (
	"os"
	"os/exec"
)

type ShellRunner struct {
	PorchRoot string
}

func (s ShellRunner) RunCommandLine(command string, args ...string) error {

	os.Setenv("PATH", os.Getenv("PATH")+":"+s.PorchRoot+"/.build/")
	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

func (s ShellRunner) RunCommandLineIntoFile(file string, command string, args ...string) error {

	os.Setenv("PATH", os.Getenv("PATH")+":"+s.PorchRoot+"/.build/")
	cmd := exec.Command(command, args...)

	outfile, err := os.Create(file)
	if err != nil {
		return err
	}
	defer outfile.Close()
	cmd.Stdout = outfile
	cmd.Stderr = os.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

func (s ShellRunner) RunCommandLineFedFromFile(file string, command string, args ...string) error {

	os.Setenv("PATH", os.Getenv("PATH")+":"+s.PorchRoot+"/.build/")
	cmd := exec.Command(command, args...)

	infile, err := os.Open(file)
	if err != nil {
		return err
	}
	defer infile.Close()
	cmd.Stdin = infile
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}
