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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/utils/ptr"
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
		sendInitialEvents   bool
		expectInitial       bool
	}{
		{
			name:                "bookmarks enabled with WatchList",
			allowWatchBookmarks: true,
			sendInitialEvents:   true,
			expectInitial:       true,
		},
		{
			name:                "bookmarks enabled without WatchList",
			allowWatchBookmarks: true,
			sendInitialEvents:   false,
			expectInitial:       false,
		},
		{
			name:                "bookmarks disabled",
			allowWatchBookmarks: false,
			sendInitialEvents:   false,
			expectInitial:       false,
		},
		{
			name:                "WatchList forces bookmarks even if allowWatchBookmarks is false",
			allowWatchBookmarks: false,
			sendInitialEvents:   true,
			expectInitial:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancelFunc := context.WithCancel(context.Background())
			defer cancelFunc()

			w := &watcher{
				cancel:              cancelFunc,
				resultChan:          make(chan watch.Event, 64),
				allowWatchBookmarks: effectiveAllowWatchBookmarks(tt.allowWatchBookmarks, tt.sendInitialEvents),
				sendInitialEvents:   tt.sendInitialEvents,
			}

			r := &fakePackageReader{}
			r.Add(1)
			var filter repository.ListPackageRevisionFilter

			go w.listAndWatch(ctx, r, filter)
			r.Wait()

			// Wait for initial bookmark
			foundInitial := waitForInitialBookmark(w.resultChan, 500*time.Millisecond)

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
	bookmarkRV := waitForBookmarkRV(w.resultChan, 1*time.Second)
	cancelFunc()

	assert.Equal(t, "123", bookmarkRV, "Expected bookmark resource version '123'")
}

func TestWatcherPeriodicBookmark(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	w := &watcher{
		cancel:              cancelFunc,
		resultChan:          make(chan watch.Event, 64),
		allowWatchBookmarks: true,
		sendInitialEvents:   true,
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

	bookmarks := waitForBookmarks(w.resultChan, 2, 500*time.Millisecond)
	cancelFunc()

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

// TestCreateGenericWatch410OnPlainWatchResume tests that createGenericWatch returns
// 410 Gone when a client sends resourceVersion without sendInitialEvents (plain watch
// resume). This forces the reflector to exit its watch() loop and do a full re-list
// with sendInitialEvents=true, which correctly reconciles the informer cache via Replace.
func TestCreateGenericWatch410OnPlainWatchResume(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.WatchList, true)

	tests := []struct {
		name        string
		options     *metainternalversion.ListOptions
		expect410   bool
		description string
	}{
		{
			name: "plain watch resume - resourceVersion set, no sendInitialEvents",
			options: &metainternalversion.ListOptions{
				ResourceVersion: "some-rv.12345",
			},
			expect410:   true,
			description: "Client is resuming watch from a specific RV without sendInitialEvents - porch cannot fulfill this",
		},
		{
			name: "initial list with sendInitialEvents=true and resourceVersion",
			options: &metainternalversion.ListOptions{
				ResourceVersion:     "some-rv.12345",
				SendInitialEvents:   ptr.To(true),
				AllowWatchBookmarks: true,
			},
			expect410:   false,
			description: "Client requests full initial events - porch can fulfill this by listing all objects",
		},
		{
			name: "fresh watch - empty resourceVersion, no sendInitialEvents",
			options: &metainternalversion.ListOptions{
				ResourceVersion: "",
			},
			expect410:   false,
			description: "Empty RV means 'start fresh' - not a resume attempt",
		},
		{
			name:        "nil options",
			options:     nil,
			expect410:   false,
			description: "Nil options should be treated as a fresh watch",
		},
		{
			name: "sendInitialEvents=false with resourceVersion",
			options: &metainternalversion.ListOptions{
				ResourceVersion:   "some-rv.12345",
				SendInitialEvents: ptr.To(false),
			},
			expect410:   true,
			description: "sendInitialEvents=false with a resourceVersion is still a plain resume - porch cannot fulfill this",
		},
		{
			name: "resourceVersion=0 without sendInitialEvents",
			options: &metainternalversion.ListOptions{
				ResourceVersion: "0",
			},
			expect410:   false,
			description: "RV=0 means 'serve from cache / any version' — not a resume, porch can handle it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			r := &fakePackageReader{}
			r.Add(1)
			filter := repository.ListPackageRevisionFilter{}
			extractor := func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
				return pr.GetPackageRevision(ctx)
			}

			w, err := createGenericWatch(ctx, r, filter, extractor, tt.options)

			if tt.expect410 {
				require.Error(t, err, tt.description)
				assert.Nil(t, w, "Watch interface should be nil on 410")
				assert.True(t, apierrors.IsResourceExpired(err), "Expected 410 ResourceExpired error, got: %v", err)

				// Verify the error message is informative
				statusErr, ok := err.(*apierrors.StatusError)
				require.True(t, ok)
				assert.Contains(t, statusErr.ErrStatus.Message, "sendInitialEvents")
			} else {
				require.NoError(t, err, tt.description)
				require.NotNil(t, w, "Watch interface should not be nil")

				// Clean up the watch
				w.Stop()
				// Drain the channel to let the goroutine exit
				for range w.ResultChan() {
				}
			}
		})
	}
}

// TestCreateGenericWatchNoGoneWhenWatchListDisabled verifies that the 410 behavior
// is not active when the WatchList feature gate is disabled.
func TestCreateGenericWatchNoGoneWhenWatchListDisabled(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.WatchList, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &fakePackageReader{}
	r.Add(1)
	filter := repository.ListPackageRevisionFilter{}
	extractor := func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
		return pr.GetPackageRevision(ctx)
	}

	// With WatchList disabled, a plain watch resume should proceed (no 410).
	options := &metainternalversion.ListOptions{
		ResourceVersion: "some-rv.12345",
	}

	w, err := createGenericWatch(ctx, r, filter, extractor, options)
	require.NoError(t, err, "Should not return 410 when WatchList feature gate is disabled")
	require.NotNil(t, w)
	w.Stop()
	for range w.ResultChan() {
	}
}

// TestCreateGenericWatch410ErrorCodeAndReason verifies the specific HTTP status code
// and reason in the 410 error response, which is what client-go's reflector uses
// to decide to exit the watch loop and trigger a re-list.
func TestCreateGenericWatch410ErrorCodeAndReason(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.WatchList, true)

	ctx := context.Background()

	r := &fakePackageReader{}
	r.Add(1)
	filter := repository.ListPackageRevisionFilter{}
	extractor := func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
		return pr.GetPackageRevision(ctx)
	}

	options := &metainternalversion.ListOptions{
		ResourceVersion: "test-repo.pkg-name.v1.1234567890",
	}

	w, err := createGenericWatch(ctx, r, filter, extractor, options)
	require.Nil(t, w)
	require.Error(t, err)

	// Verify it's a proper StatusError with correct code
	statusErr, ok := err.(*apierrors.StatusError)
	require.True(t, ok, "Error should be a StatusError")
	assert.Equal(t, int32(410), statusErr.ErrStatus.Code)
	assert.Equal(t, metav1.StatusReasonExpired, statusErr.ErrStatus.Reason)
}

// TestCreateGenericWatchAllowsWatchWithSendInitialEvents verifies that watches
// with sendInitialEvents=true proceed normally (list all objects as ADDED events
// followed by a bookmark).
func TestCreateGenericWatchAllowsWatchWithSendInitialEvents(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.WatchList, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	packages := []repository.PackageRevision{
		createFakePackageRevision("rv1"),
		createFakePackageRevision("rv2"),
	}

	r := &fakePackageReader{packages: packages}
	r.Add(1)
	filter := repository.ListPackageRevisionFilter{}
	extractor := func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
		return pr.GetPackageRevision(ctx)
	}

	options := &metainternalversion.ListOptions{
		ResourceVersion:     "old-rv.12345",
		SendInitialEvents:   ptr.To(true),
		AllowWatchBookmarks: true,
	}

	w, err := createGenericWatch(ctx, r, filter, extractor, options)
	require.NoError(t, err)
	require.NotNil(t, w)
	defer w.Stop()

	// Should receive 2 ADDED events + 1 bookmark
	events := collectEventsUntilBookmark(w.ResultChan(), 2*time.Second)
	cancel()

	require.GreaterOrEqual(t, len(events), 3, "Expected at least 2 ADDED + 1 bookmark, got %d events", len(events))

	// First two should be ADDED
	assert.Equal(t, watch.Added, events[0].Type)
	assert.Equal(t, watch.Added, events[1].Type)

	// Last should be a bookmark with initial-events-end annotation
	lastEvent := events[len(events)-1]
	assert.Equal(t, watch.Bookmark, lastEvent.Type)
	obj, ok := lastEvent.Object.(*porchapi.PackageRevision)
	require.True(t, ok)
	assert.Equal(t, "true", obj.Annotations["k8s.io/initial-events-end"])
}

// waitForInitialBookmark reads from the channel until an initial-events-end
// bookmark is found, the channel closes, or the timeout elapses.
func waitForInitialBookmark(ch chan watch.Event, timeout time.Duration) bool {
	timer := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return false
			}
			if ev.Type == watch.Bookmark {
				if obj, ok := ev.Object.(*porchapi.PackageRevision); ok {
					if obj.Annotations != nil && obj.Annotations["k8s.io/initial-events-end"] == "true" {
						return true
					}
				}
			}
		case <-timer:
			return false
		}
	}
}

// waitForBookmarkRV reads from the channel until a bookmark event is found and
// returns its resourceVersion, or returns empty string on timeout.
func waitForBookmarkRV(ch chan watch.Event, timeout time.Duration) string {
	timer := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			if ev.Type == watch.Bookmark {
				if obj, ok := ev.Object.(*porchapi.PackageRevision); ok {
					return obj.ResourceVersion
				}
			}
		case <-timer:
			return ""
		}
	}
}

// waitForBookmarks reads from the channel until at least `count` bookmark events
// are collected or the timeout elapses.
func waitForBookmarks(ch chan watch.Event, count int, timeout time.Duration) []watch.Event {
	timer := time.After(timeout)
	var bookmarks []watch.Event
	for {
		select {
		case ev := <-ch:
			if ev.Type == watch.Bookmark {
				bookmarks = append(bookmarks, ev)
				if len(bookmarks) >= count {
					return bookmarks
				}
			}
		case <-timer:
			return bookmarks
		}
	}
}

// collectEventsUntilBookmark reads events from the channel until a bookmark is
// received, the channel closes, or the timeout elapses.
func collectEventsUntilBookmark(ch <-chan watch.Event, timeout time.Duration) []watch.Event {
	timer := time.After(timeout)
	var events []watch.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
			if ev.Type == watch.Bookmark {
				return events
			}
		case <-timer:
			return events
		}
	}
}
