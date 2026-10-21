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
// limitations under the License

package v1alpha1

import "fmt"

func (gr *GitRepository) IsCurrentRepoURL(repoUrl string) bool {
	return repoUrl == gr.Repo
}

func (gr *GitRepository) GetCurrentRepoURL(repoUrl string) (string, bool, error) {
	if repoUrl == gr.Repo {
		return gr.Repo, false, nil
	}

	for _, previousRepo := range gr.PreviousRepos {
		if repoUrl == previousRepo {
			return gr.Repo, true, nil
		}
	}

	return "", false, fmt.Errorf("specified repo URL %q not found in repository specification", repoUrl)
}
