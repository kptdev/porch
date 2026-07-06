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

package api

import (
	"context"
	"fmt"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// TestWatchReturns410OnPlainResume verifies that porch returns 410 Gone when
// a watch request is made with a resourceVersion but without sendInitialEvents.
// This is the key behavior that forces client-go's reflector to do a full re-list
// (Replace) instead of a plain watch resume, which prevents stale cache entries.
func (t *PorchSuite) TestWatchReturns410OnPlainResume() {
	const repoName = "watch-410-test"

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a package so we have at least one PackageRevision with a real RV
	pr := t.CreatePackageDraftF(repoName, "test-pkg", defaultWorkspace)

	// Get the resource version from the created PackageRevision
	rv := pr.ResourceVersion
	require.NotEmpty(t.T(), rv, "PackageRevision should have a resourceVersion")

	// Create a dynamic client for making raw watch requests
	dynamicClient, err := dynamic.NewForConfig(t.Kubeconfig)
	require.NoError(t.T(), err)

	gvr := schema.GroupVersionResource{
		Group:    porchapi.SchemeGroupVersion.Group,
		Version:  porchapi.SchemeGroupVersion.Version,
		Resource: "packagerevisions",
	}

	// Test 1: Plain watch resume (resourceVersion set, no sendInitialEvents)
	// This should return 410 Gone.
	t.Run("plain_watch_resume_returns_410", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// A plain watch resume sends resourceVersion without sendInitialEvents.
		// The dynamic client's Watch method sets watch=true and resourceVersion.
		// It does NOT set sendInitialEvents by default — this mimics a reflector
		// that's been in watch() mode and is trying to resume.
		watcher, err := dynamicClient.Resource(gvr).Namespace(t.Namespace).Watch(ctx, metav1.ListOptions{
			ResourceVersion: rv,
			// No SendInitialEvents — this is the key: a plain watch resume
		})

		// Porch should return 410 (ResourceExpired), which surfaces as an error from Watch()
		if err != nil {
			// Expected: 410 ResourceExpired error (NewResourceExpired uses StatusReasonExpired)
			assert.True(t.T(), apierrors.IsResourceExpired(err),
				"Expected 410 ResourceExpired error for plain watch resume, got: %v", err)
			assert.Contains(t.T(), err.Error(), "sendInitialEvents",
				"Error message should mention sendInitialEvents")

			// Verify the actual HTTP status code is 410
			statusErr, ok := err.(*apierrors.StatusError)
			require.True(t.T(), ok, "Error should be a *StatusError")
			assert.Equal(t.T(), int32(410), statusErr.ErrStatus.Code,
				"Expected HTTP status code 410, got: %d", statusErr.ErrStatus.Code)
			return
		}

		// If Watch() didn't return an error directly, it might come as an ERROR event
		// on the watch stream (some API server versions wrap 410 as a watch event)
		defer watcher.Stop()
		select {
		case ev, ok := <-watcher.ResultChan():
			if !ok {
				t.Fatalf("Watch channel closed unexpectedly without an error event")
			}
			if ev.Type == watch.Error {
				// 410 came as an error event on the stream - this is also valid
				t.Logf("Received error event on watch stream (expected): %v", ev.Object)
			} else {
				t.Errorf("Expected 410 Gone or error event, but got event type %s", ev.Type)
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for watch response - expected immediate 410")
		}
	})

	// Test 2: Watch with sendInitialEvents=true should succeed and deliver events
	t.Run("watch_with_sendInitialEvents_succeeds", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sendInitialEvents := true
		watcher, err := dynamicClient.Resource(gvr).Namespace(t.Namespace).Watch(ctx, metav1.ListOptions{
			ResourceVersion:      rv,
			ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			SendInitialEvents:    &sendInitialEvents,
			AllowWatchBookmarks:  true,
		})
		require.NoError(t.T(), err, "Watch with sendInitialEvents=true should succeed")
		defer watcher.Stop()

		// Should receive ADDED events for existing PackageRevisions + a BOOKMARK
		addedCount, gotBookmark := consumeWatchEventsUntilBookmark(t, watcher, ctx)

		assert.GreaterOrEqual(t.T(), addedCount, 1,
			"Should receive at least 1 ADDED event (the package we created)")
		assert.True(t.T(), gotBookmark,
			"Should receive a BOOKMARK event marking end of initial events")
	})

	// Test 3: Fresh watch (empty resourceVersion) should succeed
	t.Run("fresh_watch_succeeds", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sendInitialEvents := true
		watcher, err := dynamicClient.Resource(gvr).Namespace(t.Namespace).Watch(ctx, metav1.ListOptions{
			ResourceVersion:      "",
			ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			SendInitialEvents:    &sendInitialEvents,
			AllowWatchBookmarks:  true,
		})
		require.NoError(t.T(), err, "Fresh watch should succeed")
		defer watcher.Stop()

		// Should receive events
		select {
		case ev, ok := <-watcher.ResultChan():
			if !ok {
				t.Fatalf("Watch channel closed unexpectedly")
			}
			assert.NotEqual(t.T(), watch.Error, ev.Type,
				"Fresh watch should not produce error events")
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for first watch event")
		}
	})
}

// TestWatchCacheHealsAfterReconnect verifies the end-to-end behavior:
// an informer that loses its watch connection correctly heals its cache
// when porch returns 410 on the plain resume attempt, forcing a full re-list.
func (t *PorchSuite) TestWatchCacheHealsAfterReconnect() {
	const repoName = "watch-heal-test"

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create initial packages so the informer has something in its cache
	pr1 := t.CreatePackageDraftF(repoName, "pkg-one", defaultWorkspace)
	require.NotEmpty(t.T(), pr1.Name)

	// Create a dynamic client for watch operations
	dynamicClient, err := dynamic.NewForConfig(t.Kubeconfig)
	require.NoError(t.T(), err)

	gvr := schema.GroupVersionResource{
		Group:    porchapi.SchemeGroupVersion.Group,
		Version:  porchapi.SchemeGroupVersion.Version,
		Resource: "packagerevisions",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Step 1: Start a watch with sendInitialEvents to get the initial state
	sendInitialEvents := true
	watcher1, err := dynamicClient.Resource(gvr).Namespace(t.Namespace).Watch(ctx, metav1.ListOptions{
		ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
		SendInitialEvents:    &sendInitialEvents,
		AllowWatchBookmarks:  true,
	})
	require.NoError(t.T(), err)

	// Collect all initial ADDED events and the bookmark
	var lastRV string
	var initialNames []string
	initialNames, lastRV = collectWatchNames(t, watcher1, ctx)
	watcher1.Stop()

	require.NotEmpty(t.T(), lastRV, "Should have received a resourceVersion from the initial watch")
	t.Logf("Initial watch complete: %d objects, lastRV=%s", len(initialNames), lastRV)

	// Step 2: Create another package while the watch is disconnected
	pr2 := t.CreatePackageDraftF(repoName, "pkg-two", defaultWorkspace)
	require.NotEmpty(t.T(), pr2.Name)
	t.Logf("Created pkg-two while disconnected: %s", pr2.Name)

	// Step 3: Try a plain watch resume (simulating what a reflector does after disconnect)
	// This should get 410, proving that the client MUST do a full re-list.
	_, err = dynamicClient.Resource(gvr).Namespace(t.Namespace).Watch(ctx, metav1.ListOptions{
		ResourceVersion: lastRV,
		// No SendInitialEvents — plain resume
	})
	require.Error(t.T(), err, "Plain watch resume should fail with 410")
	assert.True(t.T(), apierrors.IsResourceExpired(err),
		"Error should be 410 ResourceExpired, got: %v", err)
	statusErr, ok := err.(*apierrors.StatusError)
	require.True(t.T(), ok, "Error should be a *StatusError")
	assert.Equal(t.T(), int32(410), statusErr.ErrStatus.Code,
		"Expected HTTP status code 410, got: %d", statusErr.ErrStatus.Code)

	// Step 4: Do the correct reconnection (sendInitialEvents=true) — what the reflector
	// does after receiving 410 (it exits watch() and goes back to ListAndWatch)
	watcher2, err := dynamicClient.Resource(gvr).Namespace(t.Namespace).Watch(ctx, metav1.ListOptions{
		ResourceVersion:      lastRV,
		ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
		SendInitialEvents:    &sendInitialEvents,
		AllowWatchBookmarks:  true,
	})
	require.NoError(t.T(), err)
	defer watcher2.Stop()

	// Step 5: Verify we receive the NEW package (pkg-two) in the re-list
	reconnectNames, _ := collectWatchNames(t, watcher2, ctx)

	// The reconnected watch should contain pkg-two (created while disconnected)
	foundPkgTwo := false
	for _, name := range reconnectNames {
		if name == pr2.Name {
			foundPkgTwo = true
			break
		}
	}
	assert.True(t.T(), foundPkgTwo,
		fmt.Sprintf("Reconnected watch with sendInitialEvents should include pkg-two (%s) that was created during disconnect. Got names: %v",
			pr2.Name, reconnectNames))

	// The reconnected watch should have MORE objects than the initial watch
	// (because pkg-two was created in between)
	t.Logf("Reconnect watch: %d objects (initial had %d)", len(reconnectNames), len(initialNames))
	assert.Greater(t.T(), len(reconnectNames), len(initialNames),
		fmt.Sprintf("Reconnect should have more objects than initial watch (initial=%d, reconnect=%d)",
			len(initialNames), len(reconnectNames)))
}

// consumeWatchEventsUntilBookmark reads events from the watcher until a BOOKMARK
// is received or the context expires. Returns the count of ADDED events and
// whether a bookmark was received.
func consumeWatchEventsUntilBookmark(t *PorchSuite, watcher watch.Interface, ctx context.Context) (int, bool) {
	var addedCount int
	for {
		select {
		case ev, ok := <-watcher.ResultChan():
			if !ok {
				t.Fatalf("Watch channel closed unexpectedly")
				return addedCount, false
			}
			switch ev.Type {
			case watch.Added:
				addedCount++
			case watch.Bookmark:
				return addedCount, true
			case watch.Error:
				t.Fatalf("Unexpected error event: %v", ev.Object)
				return addedCount, false
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for watch events (got %d ADDED, bookmark=false)", addedCount)
			return addedCount, false
		}
	}
}

// collectWatchNames reads ADDED events from the watcher until a BOOKMARK is
// received or the context expires. Returns the collected object names and the
// last observed resourceVersion.
func collectWatchNames(t *PorchSuite, watcher watch.Interface, ctx context.Context) ([]string, string) {
	var names []string
	var lastRV string
	for {
		select {
		case ev, ok := <-watcher.ResultChan():
			require.True(t.T(), ok, "Watch channel closed unexpectedly")
			switch ev.Type {
			case watch.Added:
				obj, ok := ev.Object.(metav1.Object)
				require.True(t.T(), ok)
				names = append(names, obj.GetName())
				lastRV = obj.GetResourceVersion()
			case watch.Bookmark:
				obj, ok := ev.Object.(metav1.Object)
				require.True(t.T(), ok)
				lastRV = obj.GetResourceVersion()
				return names, lastRV
			case watch.Error:
				t.Fatalf("Unexpected error during watch: %v", ev.Object)
				return names, lastRV
			}
		case <-ctx.Done():
			t.Fatalf("Timeout during watch")
			return names, lastRV
		}
	}
}
