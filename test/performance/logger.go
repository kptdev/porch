// Copyright 2024, 2026 The kpt Authors
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
	"testing"
	"time"
)

type TestLogger struct {
	t      *testing.T
	file   *os.File
	logger *log.Logger
}

func NewTestLogger(t *testing.T) (*TestLogger, error) {
	// Creates logs directory if it doesn't exist
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %v", err)
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02-15-04-05")
	filename := filepath.Join(logsDir, fmt.Sprintf("porch-metrics-%s.log", timestamp))
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %v", err)
	}

	logger := log.New(file, "", 0) // Remove timestamp from log entries
	return &TestLogger{
		t:      t,
		file:   file,
		logger: logger,
	}, nil
}

func (l *TestLogger) Close() error {
	return l.file.Close()
}

func (l *TestLogger) LogResult(format string, args ...interface{}) {
	if len(args) == 0 {
		// If no args provided, treat format as plain string
		l.logger.Println(format)
	} else {
		// Use as format string with args
		l.logger.Printf(format, args...)
	}
}
