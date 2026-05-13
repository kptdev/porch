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

package context

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestGetRequestID(t *testing.T) {
	id := uuid.New()
	ctx := WithRequestID(context.Background(), id)
	if got := GetRequestID(ctx); got != id {
		t.Errorf("got %v, want %v", got, id)
	}
	//nolint:SA1012 // intentionally testing nil context handling
	if got := GetRequestID(nil); got != uuid.Nil {
		t.Errorf("got %v, want uuid.Nil", got)
	}
}

func TestWithNewRequestID(t *testing.T) {
	ctx := WithNewRequestID(context.Background())
	if got := GetRequestID(ctx); got == uuid.Nil {
		t.Error("expected non-nil UUID")
	}
}

func TestGetPackageRevision(t *testing.T) {
	ctx := WithPackageRevision(context.Background(), "test-pr")
	if got := GetPackageRevision(ctx); got != "test-pr" {
		t.Errorf("got %v, want test-pr", got)
	}
	//nolint:SA1012 // intentionally testing nil context handling
	if got := GetPackageRevision(nil); got != EmptyPRName {
		t.Errorf("got %v, want %v", got, EmptyPRName)
	}
}

func TestWithNewRequestIDAndPackageRevision(t *testing.T) {
	ctx := WithNewRequestIDAndPackageRevision(context.Background(), "test-pr")
	if got := GetRequestID(ctx); got == uuid.Nil {
		t.Error("expected non-nil UUID")
	}
	if got := GetPackageRevision(ctx); got != "test-pr" {
		t.Errorf("got %v, want test-pr", got)
	}
}

func TestLogMetadataFrom(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"nil context", nil, 0}, //nolint:SA1012
		{"empty context", context.Background(), 0},
		{"with requestID", WithRequestID(context.Background(), uuid.New()), 2},
		{"with packageRevision", WithPackageRevision(context.Background(), "test-pr"), 2},
		{"with both", WithPackageRevision(WithRequestID(context.Background(), uuid.New()), "test-pr"), 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LogMetadataFrom(tt.ctx); len(got) != tt.want {
				t.Errorf("got len %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestLogMetadataFromWithExtras(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		extras []any
		want   int
	}{
		{"nil context, no extras", nil, nil, 0}, //nolint:SA1012
		{"empty context, no extras", context.Background(), nil, 0},
		{"empty context, even extras", context.Background(), []any{"key", "value"}, 2},
		{"empty context, odd extras", context.Background(), []any{"key"}, 2},
		{"with requestID, no extras", WithRequestID(context.Background(), uuid.New()), nil, 2},
		{"with requestID, even extras", WithRequestID(context.Background(), uuid.New()), []any{"key", "value"}, 4},
		{"with requestID, odd extras", WithRequestID(context.Background(), uuid.New()), []any{"key"}, 4},
		{"with both, even extras", WithPackageRevision(WithRequestID(context.Background(), uuid.New()), "test-pr"), []any{"key1", "value1", "key2", "value2"}, 8},
		{"with both, odd extras", WithPackageRevision(WithRequestID(context.Background(), uuid.New()), "test-pr"), []any{"key1", "value1", "key2"}, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LogMetadataFromWithExtras(tt.ctx, tt.extras...)
			if len(got) != tt.want {
				t.Errorf("got len %d, want %d", len(got), tt.want)
			}
			// Verify odd extras get padded with "<no-value>"
			if len(tt.extras)%2 != 0 && len(got) > 0 {
				if got[len(got)-1] != "<no-value>" {
					t.Errorf("expected last element to be '<no-value>', got %v", got[len(got)-1])
				}
			}
		})
	}
}
