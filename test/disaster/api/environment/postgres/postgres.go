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
package postgres

import (
	"os"

	"github.com/kptdev/porch/test/disaster/api/environment/kind"
	"github.com/kptdev/porch/test/disaster/api/environment/shell"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	namespace  = "porch-system"
	podName    = "porch-postgresql-0"
	pvcName    = "data-porch-postgresql-0"
	dumpScript = `export PGPASSFILE="/tmp/.pgpass"; echo "localhost:5432:porch:porch:porch" > "$PGPASSFILE"; chmod 600 "$PGPASSFILE"; pg_dump -U porch -h localhost -d porch`
	psqlScript = `export PGPASSFILE="/tmp/.pgpass"; echo "localhost:5432:porch:porch:porch" > "$PGPASSFILE"; chmod 600 "$PGPASSFILE"; psql -U porch -h localhost -d porch`

	dumpFile     = "./dumped_db.sql"
	wipeDBScript = "/api/sql/porch-db-cleardown.sql"
)

func Backup(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Backing up Postgres cache database to file %q", dumpFile)

	if err := os.RemoveAll(dumpFile); err != nil {
		t.Fatalf("error backing up Porstres: error deleting previous backup: %w", err)
	}

	kind.UseDataCluster(t)
	args := append([]string{"exec", "-n", namespace, "-i", podName, "--", "bash", "-c"}, dumpScript)
	if err := (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLineIntoFile(dumpFile, "kubectl", args...); err != nil {
		t.Fatalf("error backing up Postgres cache database: %w", err)
	}

	t.Logf("Backed up Postgres cache database to file %q", dumpFile)
}

func Wipe(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Wiping Postgres cache database...")

	kind.UseDataCluster(t)
	args := append([]string{"exec", "-n", namespace, "-i", podName, "--", "bash", "-c"}, psqlScript)
	if err := (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLineFedFromFile(t.PorchRoot+wipeDBScript, "kubectl", args...); err != nil {
		t.Fatalf("error wiping Postgres cache database: %w", err)
	}

	t.Logf("Wiped Postgres cache database; restarting Postgres pod")

	if err := t.KubeClient.CoreV1().PersistentVolumeClaims("porch-system").Delete(t.GetContext(), pvcName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("error restarting Postgres pod: error deleting PVC: %w", err)
	}
	pvc, err := t.KubeClient.CoreV1().PersistentVolumeClaims("porch-system").Get(t.GetContext(), pvcName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("error restarting Postgres pod: error getting PVC to remove finalizers: %w", err)
	}

	pvc.Finalizers = []string{}
	if _, err := t.KubeClient.CoreV1().PersistentVolumeClaims("porch-system").Update(t.GetContext(), pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("error restarting Postgres pod: error removing finalizers from PVC: %w", err)
	}

	if err := t.KubeClient.CoreV1().Pods("porch-system").Delete(t.GetContext(), podName, metav1.DeleteOptions{GracePeriodSeconds: new(int64)}); err != nil {
		t.Fatalf("error restarting Postgres pod: error deleting %s pod: %w", podName, err)
	}

	watcher, err := t.KubeClient.CoreV1().Pods("porch-system").Watch(t.GetContext(), metav1.ListOptions{
		FieldSelector: "metadata.name=" + podName,
	})
	if err != nil {
		t.Logf("error after wiping Postgres: error watching recreated postgres pod: %w", err)
	}
	t.Logf("Waiting for Postgres pod...")
	for event := range watcher.ResultChan() {
		p, ok := event.Object.(*corev1.Pod)
		if !ok {
			t.Logf("unexpected type")
		}
		if len(p.Status.ContainerStatuses) > 0 && p.Status.ContainerStatuses[0].Ready == true {
			watcher.Stop()
			t.Logf("Postgres pod back to Ready")
			break
		}
		t.Logf("Still waiting for Postgres pod...")
	}

	t.Logf("Wiped Postgres")
}

func Restore(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Restoring Postgres cache database from file %q", dumpFile)

	kind.UseDataCluster(t)
	args := append([]string{"exec", "-n", namespace, "-i", podName, "--", "bash", "-c"}, psqlScript)
	if err := (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLineFedFromFile(t.PorchRoot+wipeDBScript, "kubectl", args...); err != nil {
		t.Fatalf("error clearing Postgres cache database before restore: %w", err)
	}

	if err := (shell.ShellRunner{PorchRoot: t.PorchRoot}).RunCommandLineFedFromFile(dumpFile, "kubectl", args...); err != nil {
		t.Fatalf("error restoring Postgres cache database: %w", err)
	}

	t.Logf("Restored Postgres cache database from file %q", dumpFile)
}
