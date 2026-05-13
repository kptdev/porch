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

package repository

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

// createTestRepo creates a basic Repository for testing
func createTestRepo(name, namespace string) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: 1,
		},
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo: "https://github.com/test/repo",
			},
		},
	}
}
