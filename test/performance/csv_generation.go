// Copyright 2024-2025 The Nephio Authors
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

package metrics

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"time"

	pkgerrors "github.com/pkg/errors"
)

func (t *PerfTestSuite) generateCSVResults() error {
	csvFile, err := os.Create(t.csvOptions.lifecycleCSV)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to create CSV file %s", t.csvOptions.lifecycleCSV)
	}
	defer func() { _ = csvFile.Close() }()

	writer := csv.NewWriter(csvFile)
	defer writer.Flush()

	header := []string{}
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoSuffix := fmt.Sprintf("%0*d", len(fmt.Sprintf("%d", t.testOptions.numRepos)), i)
		header = append(header,
			fmt.Sprintf("REPO-%s-PKG:REV", repoSuffix),
			fmt.Sprintf("REPO-%s-TOTAL-LIFECYCLE-DURATION", repoSuffix),
		)
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	type pkgRevResult struct {
		pkgName  string
		revision int
		totalDur time.Duration
	}

	repoResults := make(map[string][]pkgRevResult)

	t.metricsMutex.RLock()
	for repoName, testMetric := range t.metrics {
		for pkgName, revisions := range testMetric.pkgRevMetrics {
			for revNum, revMetrics := range revisions {
				var totalLifecycleDur time.Duration
				lifecycleComplete := false

				if publishedOp, ok := revMetrics.Metrics[pkgRevPublished]; ok && publishedOp.Error == nil {
					lifecycleComplete = true
				}

				if lifecycleComplete {
					for opKey, opMetric := range revMetrics.Metrics {
						if opMetric.Error != nil {
							continue
						}
						if opKey != pkgRevProposeDeletion && opKey != pkgRevDelete {
							totalLifecycleDur += opMetric.Duration
						}
					}
				}

				repoResults[repoName] = append(repoResults[repoName], pkgRevResult{
					pkgName:  pkgName,
					revision: revNum,
					totalDur: totalLifecycleDur,
				})
			}
		}

		sort.Slice(repoResults[repoName], func(i, j int) bool {
			if repoResults[repoName][i].pkgName != repoResults[repoName][j].pkgName {
				return repoResults[repoName][i].pkgName < repoResults[repoName][j].pkgName
			}
			return repoResults[repoName][i].revision < repoResults[repoName][j].revision
		})
	}
	t.metricsMutex.RUnlock()

	maxRows := 0
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
		if len(repoResults[repoName]) > maxRows {
			maxRows = len(repoResults[repoName])
		}
	}

	for row := 0; row < maxRows; row++ {
		record := []string{}
		for i := 0; i < t.testOptions.numRepos; i++ {
			repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
			results := repoResults[repoName]

			if row < len(results) {
				result := results[row]
				pkgRev := fmt.Sprintf("%s:v%d", result.pkgName, result.revision)
				duration := formatDuration(result.totalDur)
				record = append(record, pkgRev, duration)
			} else {
				record = append(record, "", "0")
			}
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

func (t *PerfTestSuite) generateDetailedOperationsCSV() error {
	csvFile, err := os.Create(t.csvOptions.operationsCSV)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to create operations CSV file %s", t.csvOptions.operationsCSV)
	}
	defer func() { _ = csvFile.Close() }()

	writer := csv.NewWriter(csvFile)
	defer writer.Flush()

	header := []string{}
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoSuffix := fmt.Sprintf("%0*d", len(fmt.Sprintf("%d", t.testOptions.numRepos)), i)
		header = append(header,
			fmt.Sprintf("REPO-%s-PKG:REV", repoSuffix),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevList),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevCreate),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevResourcesGet),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevUpdate),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevGet),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevPropose),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevGetProposed),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevPublished),
		)
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	type pkgRevOps struct {
		pkgName  string
		revision int
		ops      map[string]time.Duration
	}

	repoOps := make(map[string][]pkgRevOps)

	t.metricsMutex.RLock()
	for repoName, testMetric := range t.metrics {
		for pkgName, revisions := range testMetric.pkgRevMetrics {
			for revNum, revMetrics := range revisions {
				ops := make(map[string]time.Duration)

				for opKey, opMetric := range revMetrics.Metrics {
					if opMetric.Error != nil {
						continue
					}

					if opKey != pkgRevProposeDeletion && opKey != pkgRevDelete {
						ops[opKey] = opMetric.Duration
					}
				}

				repoOps[repoName] = append(repoOps[repoName], pkgRevOps{
					pkgName:  pkgName,
					revision: revNum,
					ops:      ops,
				})
			}
		}

		sort.Slice(repoOps[repoName], func(i, j int) bool {
			if repoOps[repoName][i].pkgName != repoOps[repoName][j].pkgName {
				return repoOps[repoName][i].pkgName < repoOps[repoName][j].pkgName
			}
			return repoOps[repoName][i].revision < repoOps[repoName][j].revision
		})
	}
	t.metricsMutex.RUnlock()

	maxRows := 0
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
		if len(repoOps[repoName]) > maxRows {
			maxRows = len(repoOps[repoName])
		}
	}

	for row := 0; row < maxRows; row++ {
		record := []string{}
		for i := 0; i < t.testOptions.numRepos; i++ {
			repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
			ops := repoOps[repoName]

			if row < len(ops) {
				op := ops[row]
				pkgRev := fmt.Sprintf("%s:v%d", op.pkgName, op.revision)
				record = append(record, pkgRev)

				record = append(record,
					formatDuration(op.ops[pkgRevList]),
					formatDuration(op.ops[pkgRevCreate]),
					formatDuration(op.ops[pkgRevResourcesGet]),
					formatDuration(op.ops[pkgRevUpdate]),
					formatDuration(op.ops[pkgRevGet]),
					formatDuration(op.ops[pkgRevPropose]),
					formatDuration(op.ops[pkgRevGetProposed]),
					formatDuration(op.ops[pkgRevPublished]),
				)
			} else {
				record = append(record, "", "0", "0", "0", "0", "0", "0", "0", "0")
			}
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

func (t *PerfTestSuite) generateDeletionOperationsCSV() error {
	csvFile, err := os.Create(t.csvOptions.deletionCSV)
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to create deletion CSV file %s", t.csvOptions.deletionCSV)
	}
	defer func() { _ = csvFile.Close() }()

	writer := csv.NewWriter(csvFile)
	defer writer.Flush()

	header := []string{}
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoSuffix := fmt.Sprintf("%0*d", len(fmt.Sprintf("%d", t.testOptions.numRepos)), i)
		header = append(header,
			fmt.Sprintf("REPO-%s-PKG:REV", repoSuffix),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevProposeDeletion),
			fmt.Sprintf("REPO-%s-%s", repoSuffix, pkgRevDelete),
		)
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	type pkgRevDelOps struct {
		pkgName    string
		revision   int
		proposeDel time.Duration
		deleteDur  time.Duration
	}

	repoDeletionOps := make(map[string][]pkgRevDelOps)

	t.metricsMutex.RLock()
	for repoName, testMetric := range t.metrics {
		for pkgName, revisions := range testMetric.pkgRevMetrics {
			for revNum, revMetrics := range revisions {
				var proposeDel, deleteDur time.Duration

				if proposeDelOp, ok := revMetrics.Metrics[pkgRevProposeDeletion]; ok && proposeDelOp.Error == nil {
					proposeDel = proposeDelOp.Duration
				}
				if deleteOp, ok := revMetrics.Metrics[pkgRevDelete]; ok && deleteOp.Error == nil {
					deleteDur = deleteOp.Duration
				}

				if proposeDel > 0 || deleteDur > 0 {
					repoDeletionOps[repoName] = append(repoDeletionOps[repoName], pkgRevDelOps{
						pkgName:    pkgName,
						revision:   revNum,
						proposeDel: proposeDel,
						deleteDur:  deleteDur,
					})
				}
			}
		}

		sort.Slice(repoDeletionOps[repoName], func(i, j int) bool {
			if repoDeletionOps[repoName][i].pkgName != repoDeletionOps[repoName][j].pkgName {
				return repoDeletionOps[repoName][i].pkgName < repoDeletionOps[repoName][j].pkgName
			}
			return repoDeletionOps[repoName][i].revision < repoDeletionOps[repoName][j].revision
		})
	}
	t.metricsMutex.RUnlock()

	maxRows := 0
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
		if len(repoDeletionOps[repoName]) > maxRows {
			maxRows = len(repoDeletionOps[repoName])
		}
	}

	for row := 0; row < maxRows; row++ {
		record := []string{}
		for i := 0; i < t.testOptions.numRepos; i++ {
			repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
			ops := repoDeletionOps[repoName]

			if row < len(ops) {
				op := ops[row]
				pkgRev := fmt.Sprintf("%s:v%d", op.pkgName, op.revision)
				proposeDelStr := formatDuration(op.proposeDel)
				deleteStr := formatDuration(op.deleteDur)
				record = append(record, pkgRev, proposeDelStr, deleteStr)
			} else {
				record = append(record, "", "0", "0")
			}
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	return fmt.Sprintf("%.3f", d.Seconds())
}
