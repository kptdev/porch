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

package metrics

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	pkgerrors "github.com/pkg/errors"
)

const timeDateFormat = "2006-01-02-15-04-05"

type TestLogger struct {
	file   *os.File
	logger *log.Logger
	closed bool
	mutex  sync.Mutex
}

func (t *PerfTestSuite) NewTestLogger(prefix string, apiVersion PorchAPIVersion) (*TestLogger, error) {
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to create logs directory %s", logsDir)
	}

	timestamp := time.Now().Format(timeDateFormat)
	filename := filepath.Join(logsDir, fmt.Sprintf("%s-%s-%s.log", prefix, apiVersion, timestamp))

	absPath, _ := filepath.Abs(filename)
	fmt.Printf("Creating test log file: %s\n", absPath)

	file, err := os.Create(filename)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to create log file %s", filename)
	}

	logger := log.New(file, "", 0)
	return &TestLogger{
		file:   file,
		logger: logger,
	}, nil
}

func (l *TestLogger) Sync() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.closed || l.file == nil {
		return nil
	}
	return l.file.Sync()
}

func (l *TestLogger) Close() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.closed || l.file == nil {
		return nil
	}

	if err := l.file.Sync(); err != nil {
		l.closed = true
		_ = l.file.Close()
		return err
	}

	l.closed = true
	return l.file.Close()
}

func (l *TestLogger) LogResult(format string, args ...interface{}) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.closed || l.file == nil {
		return
	}

	l.logger.Printf(format, args...)
	_ = l.file.Sync()
}

type ResultsLogger struct {
	resultsFile       *os.File
	logFile           *os.File
	mutex             sync.Mutex
	resultsFileClosed bool
	logFileClosed     bool
}

func (t *PerfTestSuite) NewResultsLogger(resultsFileName, logFileName string) (*ResultsLogger, error) {
	resultsFile, err := os.Create(resultsFileName)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to create results file %s", resultsFileName)
	}

	logFile, err := os.Create(logFileName)
	if err != nil {
		_ = resultsFile.Close()
		return nil, pkgerrors.Wrapf(err, "failed to create log file %s", logFileName)
	}

	return &ResultsLogger{
		resultsFile: resultsFile,
		logFile:     logFile,
	}, nil
}

func (l *ResultsLogger) LogTestConfig(apiVersion PorchAPIVersion, opts TestOptions, enablePrometheus bool) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.logFileClosed || l.logFile == nil {
		return
	}

	timestamp := time.Now().Format(timeDateFormat)
	_, _ = fmt.Fprintf(l.logFile,
		"%s Performance test configuration: api_version=%s namespace=%s repos=%d packages=%d revisions=%d repo_parallelism=%d package_parallelism=%d enable_deletion=%v enable_prometheus=%v\n",
		timestamp, apiVersion, opts.namespace, opts.numRepos, opts.numPkgs, opts.numRevs,
		opts.repoParallelism, opts.packageParallelism, opts.enableDeletion, enablePrometheus)
	_ = l.logFile.Sync()
}

func (l *ResultsLogger) LogApproved(repoName, pkgName string, revision int, prName string, duration time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.resultsFileClosed || l.resultsFile == nil {
		return
	}

	timestamp := time.Now().Format(timeDateFormat)
	line := fmt.Sprintf("%s %s:%s:%d %s approved, took %.3f seconds\n",
		timestamp, repoName, pkgName, revision, prName, duration.Seconds())
	_, _ = l.resultsFile.WriteString(line)
	_ = l.resultsFile.Sync()
}

func (l *ResultsLogger) LogDeleted(prName string, duration time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.resultsFileClosed || l.resultsFile == nil {
		return
	}

	timestamp := time.Now().Format(timeDateFormat)
	line := fmt.Sprintf("%s %s deleted, took %.3f seconds\n",
		timestamp, prName, duration.Seconds())
	_, _ = l.resultsFile.WriteString(line)
	_ = l.resultsFile.Sync()
}

func (l *ResultsLogger) LogToFile(format string, args ...interface{}) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.logFileClosed || l.logFile == nil {
		return
	}

	_, _ = fmt.Fprintf(l.logFile, format+"\n", args...)
	_ = l.logFile.Sync()
}

func (l *ResultsLogger) Sync() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if !l.resultsFileClosed && l.resultsFile != nil {
		if err := l.resultsFile.Sync(); err != nil {
			return err
		}
	}

	if !l.logFileClosed && l.logFile != nil {
		if err := l.logFile.Sync(); err != nil {
			return err
		}
	}

	return nil
}

func (l *ResultsLogger) Close() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	var lastErr error

	if !l.resultsFileClosed && l.resultsFile != nil {
		_ = l.resultsFile.Sync()
		if err := l.resultsFile.Close(); err != nil {
			lastErr = err
		}
		l.resultsFileClosed = true
	}

	if !l.logFileClosed && l.logFile != nil {
		_ = l.logFile.Sync()
		if err := l.logFile.Close(); err != nil {
			lastErr = err
		}
		l.logFileClosed = true
	}

	return lastErr
}
