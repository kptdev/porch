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
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
)

// createGenericWatch creates a watch.Interface that monitors package changes.
func createGenericWatch(ctx context.Context, r packageReader, filter repository.ListPackageRevisionFilter, extractor objectExtractor, options *metainternalversion.ListOptions) (watch.Interface, error) {
	ctx, cancel := context.WithCancel(ctx)

	allowWatchBookmarks := options != nil && options.AllowWatchBookmarks
	sendInitialEvents := options != nil && options.SendInitialEvents != nil && *options.SendInitialEvents
	hasExactResourceVersion := options != nil && len(options.ResourceVersion) > 0 && options.ResourceVersion != "0"
	allowWatchBookmarks = effectiveAllowWatchBookmarks(allowWatchBookmarks, sendInitialEvents)

	// A non-zero resourceVersion without sendInitialEvents is a plain watch resume.
	// Porch doesn't support RV-based resumption; return 410 to force a full re-list.
	if utilfeature.DefaultFeatureGate.Enabled(features.WatchList) && hasExactResourceVersion && !sendInitialEvents {
		klog.V(2).Infof("watch: returning 410 Gone for plain watch resume (resourceVersion=%q)", options.ResourceVersion)
		cancel()
		return nil, apierrors.NewResourceExpired("resourceVersion is not supported for watch without sendInitialEvents")
	}

	w := &watcher{
		cancel:              cancel,
		resultChan:          make(chan watch.Event, 64),
		extractor:           extractor,
		allowWatchBookmarks: allowWatchBookmarks,
		sendInitialEvents:   sendInitialEvents,
		bookmarkInterval:    1 * time.Minute, // Default bookmark interval which aligns with Kubernetes standards. Enables code to be tested.
	}
	go w.listAndWatch(ctx, r, filter)

	return w, nil
}

// Watch supports watching for changes.
func (r *packageRevisions) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	// 'label' selects on labels; 'field' selects on the object's fields. Not all fields
	// are supported; an error should be returned if 'field' tries to select on a field that
	// isn't supported. 'resourceVersion' allows for continuing/starting a watch at a
	// particular version.

	ctx, span := tracer.Start(ctx, "[START]::packageRevisions::Watch", trace.WithAttributes())
	defer span.End()

	ns, _ := genericapirequest.NamespaceFrom(ctx)

	filter, err := parsePackageRevisionFieldSelector(options, ns)
	if err != nil {
		return nil, err
	}

	return createGenericWatch(ctx, r, *filter, func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
		return pr.GetPackageRevision(ctx)
	}, options)
}

// watcher implements watch.Interface, and holds the state for an active watch.
type watcher struct {
	cancel     func()
	resultChan chan watch.Event

	// mutex that protects the eventCallback and done fields
	// from concurrent access.
	mutex         sync.Mutex
	eventCallback func(eventType watch.EventType, pr repository.PackageRevision) bool
	done          bool
	totalSent     int

	// objectExtractor function to get the appropriate object from PackageRevision
	extractor           objectExtractor
	allowWatchBookmarks bool
	sendInitialEvents   bool
	lastResourceVersion string
	initialEventsSent   bool
	bookmarkInterval    time.Duration
}

var _ watch.Interface = &watcher{}

// Stop stops watching. Will close the channel returned by ResultChan(). Releases
// any resources used by the watch.
func (w *watcher) Stop() {
	w.cancel()
}

// ResultChan returns a chan which will receive all the events. If an error occurs
// or Stop() is called, the implementation will close this channel and
// release any resources used by the watch.
func (w *watcher) ResultChan() <-chan watch.Event {
	return w.resultChan
}

type packageReader interface {
	watchPackages(ctx context.Context, filter repository.ListPackageRevisionFilter, callback engine.ObjectWatcher) error
	listPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter, callback func(ctx context.Context, p repository.PackageRevision) error) error
}

// objectExtractor transforms a repository.PackageRevision into the appropriate
// resource (PackageRevision or others when implemented).
type objectExtractor func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error)

// listAndWatch implements watch by doing a list, then sending any observed changes.
// This is not a compliant implementation of watch, but it is a good-enough start for most controllers.
// One trick is that we start the watch _before_ we perform the list, so we don't miss changes that happen immediately after the list.
func (w *watcher) listAndWatch(ctx context.Context, r packageReader, filter repository.ListPackageRevisionFilter) {
	if w.extractor == nil {
		w.extractor = func(ctx context.Context, pr repository.PackageRevision) (runtime.Object, error) {
			return pr.GetPackageRevision(ctx)
		}
	}

	if err := w.listAndWatchInner(ctx, r, filter); err != nil {
		// TODO: We need to populate the object on this error
		if err == context.Canceled || err == context.DeadlineExceeded {
			klog.V(3).Infof("sending error to watch stream: %v", err)
		} else {
			klog.Warningf("sending error to watch stream: %v", err)
		}
		// Don't send error events with nil objects
	}
	w.cancel()
	close(w.resultChan)
}

func (w *watcher) listAndWatchInner(ctx context.Context, r packageReader, filter repository.ListPackageRevisionFilter) error {
	errorResult := make(chan error, 4)

	var backlog []watch.Event
	// Make sure we hold the lock when setting the eventCallback, as it
	// will be read by other goroutines when events happen.
	w.mutex.Lock()
	w.eventCallback = func(eventType watch.EventType, pr repository.PackageRevision) bool {
		if w.done {
			return false
		}
		obj, err := w.extractor(ctx, pr)
		if err != nil {
			w.done = true
			errorResult <- err
			return false
		}
		if obj == nil {
			return true
		}

		backlog = append(backlog, watch.Event{
			Type:   eventType,
			Object: obj,
		})

		return true
	}
	w.mutex.Unlock()

	if err := r.watchPackages(ctx, filter, w); err != nil {
		return err
	}

	sentAdd := 0
	// TODO: Only if rv == 0?
	if err := r.listPackageRevisions(ctx, filter, func(ctx context.Context, p repository.PackageRevision) error {
		obj, err := w.extractor(ctx, p)
		if err != nil {
			w.mutex.Lock()
			w.done = true
			w.mutex.Unlock()
			return err
		}
		if obj == nil {
			return nil
		}
		// TODO: Check resource version?
		ev := watch.Event{
			Type:   watch.Added,
			Object: obj,
		}
		sentAdd += 1
		w.sendWatchEvent(ev)
		return nil
	}); err != nil {
		w.mutex.Lock()
		w.done = true
		w.mutex.Unlock()
		return err
	}

	// Repeatedly flush the backlog until we catch up
	sentBacklog := 0
	for {
		w.mutex.Lock()
		chunk := backlog
		backlog = nil
		w.mutex.Unlock()

		if len(chunk) == 0 {
			break
		}

		for _, ev := range chunk {
			// TODO: Check resource version?
			sentBacklog += 1
			w.sendWatchEvent(ev)
		}
	}

	w.mutex.Lock()
	// Pick up anything that squeezed in
	sentNewBacklog := 0
	for _, ev := range backlog {
		// TODO: Check resource version?

		sentNewBacklog += 1
		w.sendWatchEvent(ev)
	}

	klog.V(3).Infof("watch %p: moving watch into streaming mode after sentAdd %d, sentBacklog %d, sentNewBacklog %d", w, sentAdd, sentBacklog, sentNewBacklog)

	// Send initial bookmark after list completes if requested
	// For empty namespaces, use "0" as the resource version
	if w.allowWatchBookmarks {
		if w.lastResourceVersion == "" {
			w.lastResourceVersion = "0"
		}
		w.sendBookmark(true)
		w.initialEventsSent = true
	}

	w.eventCallback = func(eventType watch.EventType, pr repository.PackageRevision) bool {
		if w.done {
			return false
		}
		obj, err := w.extractor(ctx, pr)
		if err != nil {
			w.done = true
			errorResult <- err
			return false
		}
		if obj == nil {
			return true
		}
		// TODO: Check resource version?
		ev := watch.Event{
			Type:   eventType,
			Object: obj,
		}
		w.sendWatchEvent(ev)
		return true
	}
	w.mutex.Unlock()

	// Send periodic bookmarks if requested
	var bookmarkTicker *time.Ticker
	var bookmarkChan <-chan time.Time
	if w.allowWatchBookmarks {
		interval := w.bookmarkInterval
		if interval == 0 {
			interval = 1 * time.Minute
		}
		bookmarkTicker = time.NewTicker(interval)
		defer bookmarkTicker.Stop()
		bookmarkChan = bookmarkTicker.C
	}

	for {
		select {
		case <-ctx.Done():
			w.mutex.Lock()
			defer w.mutex.Unlock()
			w.done = true
			return ctx.Err()

		case err := <-errorResult:
			w.mutex.Lock()
			defer w.mutex.Unlock()
			w.done = true
			return err

		case <-bookmarkChan:
			w.mutex.Lock()
			w.sendBookmark(false)
			w.mutex.Unlock()
		}
	}
}

func (w *watcher) sendWatchEvent(ev watch.Event) {
	// TODO: Handle the case that the watch channel is full?
	w.resultChan <- ev
	w.totalSent += 1
	// Track resource version for bookmarks
	if obj, ok := ev.Object.(metav1.Object); ok {
		w.lastResourceVersion = obj.GetResourceVersion()
	}
}

func (w *watcher) sendBookmark(markInitialEventsEnd bool) {
	if w.done {
		return
	}

	// Create a bookmark event with the last known resource version
	if w.lastResourceVersion != "" {
		// Create a minimal PackageRevision object for the bookmark
		obj := &porchapi.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchapi.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: w.lastResourceVersion,
			},
		}
		// Add annotation to mark end of initial events only for WatchList requests
		if markInitialEventsEnd && w.sendInitialEvents {
			obj.Annotations = map[string]string{
				"k8s.io/initial-events-end": "true",
			}
		}
		ev := watch.Event{
			Type:   watch.Bookmark,
			Object: obj,
		}
		w.resultChan <- ev
		didMarkInitialEventsEnd := markInitialEventsEnd && w.sendInitialEvents
		klog.V(2).Infof("watch %p: sent bookmark with resourceVersion %s (markInitialEventsEnd=%v, sendInitialEvents=%v, annotationApplied=%v)", w, w.lastResourceVersion, markInitialEventsEnd, w.sendInitialEvents, didMarkInitialEventsEnd)
	}
}

// OnPackageRevisionChange is the callback called when a PackageRevision changes.
func (w *watcher) OnPackageRevisionChange(eventType watch.EventType, pr repository.PackageRevision) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	return w.eventCallback(eventType, pr)
}

// effectiveAllowWatchBookmarks returns true if bookmarks should be sent.
// WatchList semantics require bookmarks to signal initial-events-end.
func effectiveAllowWatchBookmarks(allowWatchBookmarks, sendInitialEvents bool) bool {
	return allowWatchBookmarks || sendInitialEvents
}
