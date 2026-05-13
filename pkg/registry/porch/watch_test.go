// Copyright 2022, 2024-2026 The kpt Authors
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

import (
	"context"
	"sync"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// Helper to create fake package revisions
func createFakePackageRevision(resourceVersion string) *fake.FakePackageRevision {
	return &fake.FakePackageRevision{
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels:          make(map[string]string),
				ResourceVersion: resourceVersion,
			},
		},
	}
}

// Common fake reader for all tests
type fakePackageReader struct {
	sync.WaitGroup
	callback           engine.ObjectWatcher
	packages           []repository.PackageRevision
	sendEventInBacklog bool
}

func (f *fakePackageReader) watchPackages(ctx context.Context, filter repository.ListPackageRevisionFilter, callback engine.ObjectWatcher) error {
	f.callback = callback
	if f.sendEventInBacklog {
		pkgRev := createFakePackageRevision("")
		callback.OnPackageRevisionChange(watch.Modified, pkgRev)
	}
	f.Done()
	return nil
}

func (f *fakePackageReader) listPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter, callback func(ctx context.Context, p repository.PackageRevision) error) error {
	for _, pkg := range f.packages {
		if err := callback(ctx, pkg); err != nil {
			return err
		}
	}
	return nil
}

func TestWatcherClose(t *testing.T) {
	ctx := context.Background()
	ctx, cancelFunc := context.WithCancel(ctx)

	w := &watcher{
		cancel:     cancelFunc,
		resultChan: make(chan watch.Event, 64),
	}

	r := &fakePackageReader{}
	r.Add(1)
	var filter repository.ListPackageRevisionFilter

	go w.listAndWatch(ctx, r, filter)

	// Just make sure someone is pulling events of the result channel.
	go func() {
		for range w.resultChan {
			// do nothing
		}
	}()

	// Wait until the callback has been set in the fakePackageReader
	r.Wait()

	// Create lots of watch events for the next 2 seconds.
	timer := time.NewTimer(2 * time.Second)
	go func() {
		ch := make(chan struct{})
		close(ch)
		for {
			select {
			case <-ch:
				pkgRev := createFakePackageRevision("")
				if cont := r.callback.OnPackageRevisionChange(watch.Modified, pkgRev); !cont {
					return
				}
			case <-timer.C:
				return
			}
		}
	}()

	// Close the watcher while watch events are being sent.
	<-time.NewTimer(1 * time.Second).C
	cancelFunc()
	<-timer.C
}

func TestWatcherNilObject(t *testing.T) {
	tests := []struct {
		name             string
		packages         []repository.PackageRevision
		waitForStreaming bool
		sendEvent        bool
	}{
		{
			name:      "backlog phase",
			packages:  nil,
			sendEvent: false,
		},
		{
			name:             "streaming phase",
			packages:         nil,
			waitForStreaming: true,
			sendEvent:        true,
		},
		{
			name: "list phase",
			packages: []repository.PackageRevision{
				createFakePackageRevision(""),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancelFunc := context.WithCancel(context.Background())
			defer cancelFunc()

			w := &watcher{
				cancel:     cancelFunc,
				resultChan: make(chan watch.Event, 64),
				extractor: func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
					return nil, nil
				},
			}

			r := &fakePackageReader{
				packages:           tt.packages,
				sendEventInBacklog: tt.name == "backlog phase",
			}
			r.Add(1)
			var filter repository.ListPackageRevisionFilter
			go w.listAndWatch(ctx, r, filter)
			r.Wait()

			if tt.waitForStreaming {
				time.Sleep(100 * time.Millisecond)
			}

			if tt.sendEvent {
				pkgRev := createFakePackageRevision("")
				cont := r.callback.OnPackageRevisionChange(watch.Modified, pkgRev)
				assert.True(t, cont, "Expected callback to return true for nil object")
			} else {
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestWatcherBookmarks(t *testing.T) {
	tests := []struct {
		name                string
		allowWatchBookmarks bool
		expectInitial       bool
	}{
		{
			name:                "bookmarks enabled",
			allowWatchBookmarks: true,
			expectInitial:       true,
		},
		{
			name:                "bookmarks disabled",
			allowWatchBookmarks: false,
			expectInitial:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancelFunc := context.WithCancel(context.Background())
			defer cancelFunc()

			w := &watcher{
				cancel:              cancelFunc,
				resultChan:          make(chan watch.Event, 64),
				allowWatchBookmarks: tt.allowWatchBookmarks,
			}

			r := &fakePackageReader{}
			r.Add(1)
			var filter repository.ListPackageRevisionFilter

			go w.listAndWatch(ctx, r, filter)
			r.Wait()

			// Wait for initial bookmark
			timeout := time.After(500 * time.Millisecond)
			foundInitial := false

			for {
				select {
				case ev, ok := <-w.resultChan:
					if !ok {
						goto done
					}
					if ev.Type == watch.Bookmark {
						if obj, ok := ev.Object.(*porchapi.PackageRevision); ok {
							if obj.Annotations != nil && obj.Annotations["k8s.io/initial-events-end"] == "true" {
								foundInitial = true
								goto done
							}
						}
					}
				case <-timeout:
					goto done
				}
			}

		done:
			cancelFunc()
			if tt.expectInitial {
				assert.True(t, foundInitial, "Expected initial bookmark but none found")
			} else {
				assert.False(t, foundInitial, "Did not expect initial bookmark but found one")
			}
		})
	}
}

func TestWatcherBookmarkResourceVersion(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	w := &watcher{
		cancel:              cancelFunc,
		resultChan:          make(chan watch.Event, 64),
		allowWatchBookmarks: true,
	}

	r := &fakePackageReader{
		packages: []repository.PackageRevision{
			createFakePackageRevision("123"),
		},
	}
	r.Add(1)
	var filter repository.ListPackageRevisionFilter

	go w.listAndWatch(ctx, r, filter)
	r.Wait()

	// Wait for initial bookmark
	timeout := time.After(1 * time.Second)
	var bookmarkRV string

	for {
		select {
		case ev := <-w.resultChan:
			if ev.Type == watch.Bookmark {
				if obj, ok := ev.Object.(*porchapi.PackageRevision); ok {
					bookmarkRV = obj.ResourceVersion
					cancelFunc()
					goto done
				}
			}
		case <-timeout:
			require.Fail(t, "Timeout waiting for bookmark")
		}
	}

done:
	assert.Equal(t, "123", bookmarkRV, "Expected bookmark resource version '123'")
}

func TestWatcherPeriodicBookmark(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	w := &watcher{
		cancel:              cancelFunc,
		resultChan:          make(chan watch.Event, 64),
		allowWatchBookmarks: true,
		bookmarkInterval:    100 * time.Millisecond,
	}

	r := &fakePackageReader{
		packages: []repository.PackageRevision{
			createFakePackageRevision("200"),
		},
	}
	r.Add(1)
	var filter repository.ListPackageRevisionFilter

	go w.listAndWatch(ctx, r, filter)
	r.Wait()

	var bookmarks []watch.Event
	timeout := time.After(500 * time.Millisecond)

	for {
		select {
		case ev := <-w.resultChan:
			if ev.Type == watch.Bookmark {
				bookmarks = append(bookmarks, ev)
				if len(bookmarks) >= 2 {
					cancelFunc()
					goto done
				}
			}
		case <-timeout:
			cancelFunc()
			goto done
		}
	}

done:
	require.GreaterOrEqual(t, len(bookmarks), 2, "Expected at least 2 bookmarks (initial + periodic)")

	// First should be initial bookmark
	if obj, ok := bookmarks[0].Object.(*porchapi.PackageRevision); ok {
		assert.NotNil(t, obj.Annotations)
		assert.Equal(t, "true", obj.Annotations["k8s.io/initial-events-end"])
	}

	// Second should be periodic bookmark without annotation
	if obj, ok := bookmarks[1].Object.(*porchapi.PackageRevision); ok {
		assert.Empty(t, obj.Annotations, "Periodic bookmark should not have annotations")
	}
}
