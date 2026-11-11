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

import pkglifecycle "github.com/kptdev/porch/pkg/lifecycle"

var registryLifecyclePolicy pkglifecycle.Options

// SetLifecyclePolicy configures lifecycle transition policy for v1alpha1 registry validation.
// Must be called before serving requests (typically from apiserver startup).
func SetLifecyclePolicy(policy pkglifecycle.Options) {
	registryLifecyclePolicy = policy
}

func registryLifecyclePolicyOrDefault() pkglifecycle.Options {
	return registryLifecyclePolicy
}
