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
package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/kptdev/porch/test/disaster/api/environment/kind"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	backupRootFolder = "./gitea_backups"
)

func Backup(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	kind.UseDataCluster(t)
	var service corev1.Service
	t.GetF(client.ObjectKey{
		Namespace: "gitea",
		Name:      "gitea-lb",
	}, &service)
	giteaIP := service.Status.LoadBalancer.Ingress[0].IP

	urlSegment := fmt.Sprintf("porch:secret@%s:3000", giteaIP)
	repoListUrl := fmt.Sprintf("http://%s/api/v1/user/repos", urlSegment)

	repoUrls := func() (cloneUrls map[string]string) {
		resp, err := http.Get(repoListUrl)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("error backing up Gitea: error getting repo list to back up: %w", err)
		}
		defer resp.Body.Close()

		jsonBody := []map[string]any{}
		if err := json.NewDecoder(resp.Body).Decode(&jsonBody); err != nil {
			t.Fatalf("error backing up Gitea: error reading repo list from response %w", err)
		}

		cloneUrls = make(map[string]string)
		regex := regexp.MustCompile(`(://).*(/porch)`)
		for _, eachRepo := range jsonBody {
			cloneUrl := regex.ReplaceAllString(
				eachRepo["clone_url"].(string),
				fmt.Sprintf("${1}%s${2}", urlSegment))
			cloneUrls[eachRepo["name"].(string)] = cloneUrl
		}
		return
	}()

	t.Logf("Backing up %d Gitea repos...", len(repoUrls))

	if err := os.RemoveAll(backupRootFolder); err != nil {
		t.Fatalf("error backing up Gitea: error deleting previous backup: %w", err)
	}
	os.Mkdir(backupRootFolder, 0777)
	for name, url := range repoUrls {
		t.Logf("Cloning repo to back up: %q", url)
		dir := fmt.Sprintf("%s/%s", backupRootFolder, name)
		if _, err := git.PlainClone(dir, false, &git.CloneOptions{
			URL:          url,
			Progress:     os.Stdout,
			Tags:         git.AllTags,
			SingleBranch: false,
		}); err != nil {
			t.Fatalf("error backing up Gitea: error cloning repo %q: %w", url, err.Error())
		}
	}

	t.Logf("Backed up Gitea repos to directory %q", backupRootFolder)
}

func Wipe(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Wiping Gitea")

	kind.UseDataCluster(t)
	if err := t.KubeClient.CoreV1().Pods("gitea").
		DeleteCollection(t.GetContext(),
			metav1.DeleteOptions{GracePeriodSeconds: new(int64)},
			metav1.ListOptions{LabelSelector: "app=gitea"}); err != nil {
		t.Fatalf("error wiping Gitea: error deleting gitea pod: %w", err)
	}
	watcher, err := t.KubeClient.CoreV1().Pods("gitea").Watch(t.GetContext(), metav1.ListOptions{
		LabelSelector: "app=gitea",
	})

	if err != nil {
		t.Logf("error after wiping Gitea: error watching recreated gitea pod: %w", err)
	}
	t.Logf("Waiting for Gitea pod...")
	for event := range watcher.ResultChan() {
		p, ok := event.Object.(*corev1.Pod)
		if !ok {
			t.Logf("unexpected type")
		}
		if len(p.Status.ContainerStatuses) > 0 && p.Status.ContainerStatuses[0].Ready == true {
			watcher.Stop()
			t.Logf("Gitea pod back to Ready")
			break
		}
		t.Logf("Still waiting for Gitea pod...")
	}

	t.Logf("Wiped Gitea")
}

func Restore(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	repoBackups, err := os.ReadDir(backupRootFolder)
	if err != nil {
		t.Fatalf("error restoring Gitea: error searching for repo backups in %q: %w", backupRootFolder, err)
	}

	t.Logf("Restoring %d Gitea repos from directory %q", len(repoBackups), backupRootFolder)

	for _, repoDir := range repoBackups {
		if repoDir.IsDir() {
			t.Logf("Pushing backed-up repo: %q", repoDir.Name())
			repo, err := git.PlainOpen(fmt.Sprintf("./%s/%s", backupRootFolder, repoDir.Name()))
			if err != nil {
				t.Fatalf("error restoring Gitea: error accessing backup folder %q: %w", repoDir.Name())
			}
			if err := repo.Push(&git.PushOptions{
				Progress: os.Stdout,
				RefSpecs: []config.RefSpec{
					config.DefaultPushRefSpec,
					config.RefSpec("refs/tags/*:refs/tags/*"),
				},
			}); err != nil {
				t.Logf("error restoring Gitea: error pushing backup repo %q back to Gitea: %w", repoDir.Name, err.Error())
			}
		}
	}

	t.Logf("Successfully restored Gitea repos")
}
