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

package packagerevision

import (
	"context"
	"fmt"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
)

const (
	// maintenanceStartDelay lets the caches and the first reconciles settle before the startup pass.
	maintenanceStartDelay = 30 * time.Second
	// maintenanceWriteEvery paces writes to 20 per second, so a large backlog never sends a burst to
	// the API server.
	maintenanceWriteEvery = 50 * time.Millisecond
	// maintenanceListPage bounds each page of the UpstreamDependents list.
	maintenanceListPage = 500
)

// maintenanceConfirmDelay is how long a reset waits before it re-reads the dependents. It covers the
// window in which the writer has updated the upstream object but not yet the dependent's status.
// A variable so tests can set it to zero.
var maintenanceConfirmDelay = 30 * time.Second

// dependentMaintenance adds missing UpstreamDependents entries on every pass, and on a sweep also removes
// stale entries, deletes empty objects and rebuilds truncated ones. It runs at startup and daily. A sweep
// confirms a dependent with an API read before removing its entry, so a lagging cache cannot drop a live
// one. Leader election is off, so every replica runs the pass. Add leader election before sharding.
type dependentMaintenance struct {
	// client reads PackageRevisions from the cache and writes the objects.
	client client.Client
	// apiReader lists UpstreamDependents in pages and confirms dependents.
	apiReader              client.Reader
	sweepHour, sweepMinute int
	startupSweep           bool
	startDelay             time.Duration
	writeEvery             time.Duration
	// now is the clock. It is time.Now except in tests.
	now func() time.Time
}

// newDependentMaintenance parses at as a UTC time of day (HH:MM) for the daily sweep.
func newDependentMaintenance(c client.Client, apiReader client.Reader, at string, startupSweep bool) (*dependentMaintenance, error) {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return nil, fmt.Errorf("dependent sweep time %q must be HH:MM in UTC: %w", at, err)
	}
	return &dependentMaintenance{
		client:       c,
		apiReader:    apiReader,
		sweepHour:    t.Hour(),
		sweepMinute:  t.Minute(),
		startupSweep: startupSweep,
		startDelay:   maintenanceStartDelay,
		writeEvery:   maintenanceWriteEvery,
		now:          time.Now,
	}, nil
}

// nextSweep returns the next occurrence of the daily sweep time after now, in UTC. A time equal to
// now is not "next", so a sweep that has just run does not repeat.
func (m *dependentMaintenance) nextSweep(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), m.sweepHour, m.sweepMinute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Start runs the startup pass once after startDelay, then a sweep pass each day at the configured
// time. It returns nil when ctx is cancelled.
func (m *dependentMaintenance) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(m.startDelay):
	}
	m.runLogged(ctx, m.startupSweep)

	logger := ctrl.Log.WithName("dependent-maintenance")
	for {
		next := m.nextSweep(m.now())
		logger.V(1).Info("next dependents sweep", "at", next.Format(time.RFC3339))
		timer := time.NewTimer(next.Sub(m.now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		m.runLogged(ctx, true)
	}
}

// runLogged runs one pass and logs its error. The next pass retries whatever it missed.
func (m *dependentMaintenance) runLogged(ctx context.Context, sweep bool) {
	if err := m.pass(ctx, sweep); err != nil && ctx.Err() == nil {
		ctrl.Log.WithName("dependent-maintenance").Error(err, "dependents pass failed; retrying on the next pass")
	}
}

// passStats counts what a pass wrote, for the log line.
type passStats struct {
	written int
	failed  int
	sweep   sweepStats
}

// sweepStats counts what a sweep changed, for the log line.
type sweepStats struct {
	removedEntries int
	deletedObjects int
	reset          int
}

// pass makes one pass over the UpstreamDependents objects and the cached PackageRevisions. It returns
// ctx's error when cancelled.
func (m *dependentMaintenance) pass(ctx context.Context, sweep bool) error {
	logger := ctrl.Log.WithName("dependent-maintenance")

	existing, err := m.listExisting(ctx)
	if err != nil {
		return err
	}
	var prs porchv1alpha2.PackageRevisionList
	if err := m.client.List(ctx, &prs); err != nil {
		return err
	}
	writer := &PackageRevisionReconciler{Client: m.client, apiReader: m.apiReader}
	wanted, err := m.wantedTargets(ctx, writer, prs.Items)
	if err != nil {
		return err
	}

	byNamespace := packageRevisionsByNamespace(prs.Items)
	objectsByNS := map[string][]*porchv1alpha2.UpstreamDependents{}
	for _, obj := range existing {
		objectsByNS[obj.Namespace] = append(objectsByNS[obj.Namespace], obj)
	}
	wantedByNS := map[string][]*wantedTarget{}
	for _, w := range wanted {
		wantedByNS[w.namespace] = append(wantedByNS[w.namespace], w)
	}
	nsSet := map[string]struct{}{}
	for ns := range objectsByNS {
		nsSet[ns] = struct{}{}
	}
	for ns := range wantedByNS {
		nsSet[ns] = struct{}{}
	}
	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	pace := time.NewTicker(m.writeEvery)
	defer pace.Stop()

	// Each namespace is finished before the next one starts, so a pass never writes across namespaces
	// at once.
	var stats passStats
	for _, ns := range namespaces {
		if err := m.passNamespace(ctx, writer, ns, objectsByNS[ns], wantedByNS[ns], byNamespace[ns],
			sweep, pace, &stats); err != nil {
			return err
		}
	}
	logger.Info("dependents pass finished", "sweep", sweep, "written", stats.written, "failed", stats.failed,
		"removedEntries", stats.sweep.removedEntries, "deletedObjects", stats.sweep.deletedObjects,
		"reset", stats.sweep.reset)
	return nil
}

// passNamespace reconciles one namespace's objects, then creates the ones it is missing.
func (m *dependentMaintenance) passNamespace(ctx context.Context, writer *PackageRevisionReconciler, namespace string,
	objs []*porchv1alpha2.UpstreamDependents, wants []*wantedTarget, prs map[string]*porchv1alpha2.PackageRevision,
	sweep bool, pace *time.Ticker, stats *passStats) error {
	logger := ctrl.Log.WithName("dependent-maintenance")
	wantByName := make(map[string]*wantedTarget, len(wants))
	for _, w := range wants {
		wantByName[w.target.objectName] = w
	}
	for _, obj := range objs {
		want := wantByName[obj.Name]
		delete(wantByName, obj.Name)
		if err := m.reconcileObject(ctx, obj, want, prs, sweep, pace, stats); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			stats.failed++
			logger.Error(err, "dependents pass write failed; the next pass retries it", "object", obj.Name)
		}
	}
	// Objects that do not exist yet are created with their entries.
	for _, w := range wantByName {
		if err := waitTick(ctx, pace); err != nil {
			return err
		}
		if err := writer.writeUpstreamDependents(ctx, namespace, w.target); err != nil {
			stats.failed++
			logger.Error(err, "dependents pass create failed; the next pass retries it", "object", w.target.objectName)
			continue
		}
		stats.written++
	}
	return nil
}

// wantedTarget is the write a missing object needs: its namespace and the target that creates it.
type wantedTarget struct {
	namespace string
	target    *upstreamTarget
}

// wantedTargets returns, keyed by namespace and object name, the entries every cached dependent
// expects on each upstream object, and the upstream's package key where it is in the cache.
func (m *dependentMaintenance) wantedTargets(ctx context.Context, writer *PackageRevisionReconciler,
	prs []porchv1alpha2.PackageRevision) (map[string]*wantedTarget, error) {
	out := map[string]*wantedTarget{}
	for i := range prs {
		pr := &prs[i]
		if pr.DeletionTimestamp != nil {
			continue
		}
		for _, t := range planUpstreamTargets(pr.Name, nil, pr.Status.SubpackageUpstreams,
			directChange{add: upstreamNames(pr)}) {
			key := pr.Namespace + "/" + t.objectName
			w := out[key]
			if w == nil {
				w = &wantedTarget{namespace: pr.Namespace, target: t}
				out[key] = w
			} else {
				w.target.add = append(w.target.add, t.add...)
			}
			if w.target.upstreamPackage == "" {
				if err := writer.resolveUpstreamPackage(ctx, pr.Namespace, w.target); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// listExisting returns every UpstreamDependents object keyed by namespace and name. It reads the
// API server in pages, so one large list is never sent in a single response.
func (m *dependentMaintenance) listExisting(ctx context.Context) (map[string]*porchv1alpha2.UpstreamDependents, error) {
	out := map[string]*porchv1alpha2.UpstreamDependents{}
	opts := []client.ListOption{client.Limit(maintenanceListPage)}
	for {
		var page porchv1alpha2.UpstreamDependentsList
		if err := m.apiReader.List(ctx, &page, opts...); err != nil {
			return nil, err
		}
		for i := range page.Items {
			obj := &page.Items[i]
			out[obj.Namespace+"/"+obj.Name] = obj
		}
		if page.Continue == "" {
			return out, nil
		}
		opts = []client.ListOption{client.Limit(maintenanceListPage), client.Continue(page.Continue)}
	}
}

// reconcileObject decides one existing object's entries and writes it at most once. A truncated
// object is rebuilt on a sweep pass and left alone otherwise.
func (m *dependentMaintenance) reconcileObject(ctx context.Context, obj *porchv1alpha2.UpstreamDependents,
	want *wantedTarget, prs map[string]*porchv1alpha2.PackageRevision, sweep bool, pace *time.Ticker,
	stats *passStats) error {
	if obj.Status.Truncated {
		if !sweep {
			return nil
		}
		if err := waitTick(ctx, pace); err != nil {
			return err
		}
		return m.rebuild(ctx, obj, prs, &stats.sweep)
	}

	merged, needsKey, err := m.desiredEntries(ctx, obj, want, prs, sweep, stats)
	if err != nil {
		return err
	}
	// A sweep also deletes an object that is left empty, so an empty result is always a write.
	if equalSet(merged, obj.Status.Dependents) && !needsKey && (!sweep || len(merged) != 0) {
		return nil
	}
	if err := waitTick(ctx, pace); err != nil {
		return err
	}
	return m.write(ctx, obj, merged, want, needsKey, stats)
}

// desiredEntries returns the entries obj should hold: the kept ones (only the live ones on a sweep)
// plus the missing ones. needsKey reports that the object lacks the package key the cache knows.
func (m *dependentMaintenance) desiredEntries(ctx context.Context, obj *porchv1alpha2.UpstreamDependents,
	want *wantedTarget, prs map[string]*porchv1alpha2.PackageRevision, sweep bool,
	stats *passStats) ([]porchv1alpha2.UpstreamDependent, bool, error) {
	kept := obj.Status.Dependents
	if sweep {
		var err error
		if kept, err = m.liveEntries(ctx, obj, prs, &stats.sweep); err != nil {
			return nil, false, err
		}
	}
	if want == nil {
		return kept, false, nil
	}
	merged, _ := applyEntries(kept, want.target.add, nil)
	return merged, obj.Spec.UpstreamPackage == "" && want.target.upstreamPackage != "", nil
}

// write saves the merged entries and, when needed, the package key. The status is written first, so
// a concurrent change to the status makes this conflict, and the spec is never written against a
// stale status.
func (m *dependentMaintenance) write(ctx context.Context, obj *porchv1alpha2.UpstreamDependents,
	merged []porchv1alpha2.UpstreamDependent, want *wantedTarget, needsKey bool, stats *passStats) error {
	if !equalSet(merged, obj.Status.Dependents) || len(merged) == 0 {
		if err := m.save(ctx, obj, merged, &stats.sweep); err != nil {
			return err
		}
	}
	if needsKey {
		// A merge patch sends only the package key. It carries no resourceVersion, so it cannot
		// conflict, and it cannot overwrite the status written above.
		base := obj.DeepCopy()
		obj.Spec.UpstreamPackage = want.target.upstreamPackage
		if err := m.client.Patch(ctx, obj, client.MergeFrom(base)); err != nil {
			return err
		}
	}
	stats.written++
	return nil
}

// packageRevisionsByNamespace groups PackageRevisions by namespace, keyed by name.
func packageRevisionsByNamespace(items []porchv1alpha2.PackageRevision) map[string]map[string]*porchv1alpha2.PackageRevision {
	out := map[string]map[string]*porchv1alpha2.PackageRevision{}
	for i := range items {
		pr := &items[i]
		if out[pr.Namespace] == nil {
			out[pr.Namespace] = map[string]*porchv1alpha2.PackageRevision{}
		}
		out[pr.Namespace][pr.Name] = pr
	}
	return out
}

// liveEntries returns the entries whose dependent still uses the upstream. The cache is trusted for a
// live match. Anything the cache does not confirm is checked with the API server.
func (m *dependentMaintenance) liveEntries(ctx context.Context, obj *porchv1alpha2.UpstreamDependents,
	prs map[string]*porchv1alpha2.PackageRevision, stats *sweepStats) ([]porchv1alpha2.UpstreamDependent, error) {
	var kept []porchv1alpha2.UpstreamDependent
	for _, e := range obj.Status.Dependents {
		if cached, ok := prs[e.Name]; ok && usesUpstream(cached, obj.Spec.UpstreamKey, e) {
			kept = append(kept, e)
			continue
		}
		live, err := m.confirmedUse(ctx, obj.Namespace, obj.Spec.UpstreamKey, e)
		if err != nil {
			return nil, err
		}
		if live {
			kept = append(kept, e)
		} else {
			stats.removedEntries++
		}
	}
	return kept, nil
}

// confirmedUse reads the dependent from the API server and reports whether it still uses the upstream.
// A dependent with truncated upstream data is kept, since its uses are unknown.
func (m *dependentMaintenance) confirmedUse(ctx context.Context, namespace, upstreamKey string,
	e porchv1alpha2.UpstreamDependent) (bool, error) {
	pr := &porchv1alpha2.PackageRevision{}
	err := m.apiReader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: e.Name}, pr)
	switch {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("confirm dependent %s/%s: %w", namespace, e.Name, err)
	}
	return usesUpstream(pr, upstreamKey, e), nil
}

// usesUpstream reports whether pr still records the entry e for the upstream identity. A dependent with
// truncated upstream data is treated as using it, because its uses are unknown.
func usesUpstream(pr *porchv1alpha2.PackageRevision, upstreamKey string, e porchv1alpha2.UpstreamDependent) bool {
	if pr.Status.SubpackageUpstreamsTruncated {
		return true
	}
	return containsEntry(upstreamEdges(pr, upstreamKey), e)
}

// rebuild recomputes a truncated object from the cached PackageRevisions in its namespace. The flag is
// cleared only when the rebuilt list is complete and under the cap. A dependent with truncated upstream
// data could hide an edge, so the object stays truncated while one exists.
func (m *dependentMaintenance) rebuild(ctx context.Context, obj *porchv1alpha2.UpstreamDependents,
	prs map[string]*porchv1alpha2.PackageRevision, stats *sweepStats) error {
	cached := make([]*porchv1alpha2.PackageRevision, 0, len(prs))
	for _, pr := range prs {
		cached = append(cached, pr)
	}
	entries, reason := rebuildEntries(cached, obj.Spec.UpstreamKey)
	if reason != "" {
		logRebuildRefused(obj, reason)
		return nil
	}

	// The writer updates the upstream object before the dependent's status, so a list taken in between
	// can miss a new entry. Read again after a delay, and reset only if the rebuilt list is unchanged.
	if err := waitFor(ctx, maintenanceConfirmDelay); err != nil {
		return err
	}
	var fresh porchv1alpha2.PackageRevisionList
	if err := m.client.List(ctx, &fresh, client.InNamespace(obj.Namespace)); err != nil {
		return err
	}
	freshPRs := make([]*porchv1alpha2.PackageRevision, 0, len(fresh.Items))
	for i := range fresh.Items {
		freshPRs = append(freshPRs, &fresh.Items[i])
	}
	confirmed, reason := rebuildEntries(freshPRs, obj.Spec.UpstreamKey)
	if reason != "" || !equalSet(confirmed, entries) {
		logRebuildRefused(obj, "dependents changed during the confirmation delay")
		return nil
	}

	obj.Status.Truncated = false
	clearCapCondition(obj)
	stats.reset++
	return m.save(ctx, obj, entries, stats)
}

// rebuildEntries returns the complete entry list for an upstream from the given PackageRevisions. It
// returns a reason instead when the list cannot be complete: a dependent's subpackage list is truncated,
// or the list is over the cap.
func rebuildEntries(prs []*porchv1alpha2.PackageRevision, upstreamKey string) ([]porchv1alpha2.UpstreamDependent, string) {
	var edges []porchv1alpha2.UpstreamDependent
	for _, pr := range prs {
		if pr.Status.SubpackageUpstreamsTruncated {
			return nil, "dependent " + pr.Name + " has a truncated subpackage list"
		}
		edges = append(edges, upstreamEdges(pr, upstreamKey)...)
	}
	entries, _ := applyEntries(nil, edges, nil)
	if len(entries) > upstreamdependents.MaxDependents {
		return nil, fmt.Sprintf("rebuilt list has %d entries, over the cap of %d",
			len(entries), upstreamdependents.MaxDependents)
	}
	return entries, ""
}

// waitFor blocks for d, or until ctx is cancelled.
func waitFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// upstreamEdges returns the edges pr records to the upstream identity: a whole-package edge from its
// clone or upgrade, and one edge per matching subpackage.
func upstreamEdges(pr *porchv1alpha2.PackageRevision, upstreamKey string) []porchv1alpha2.UpstreamDependent {
	var edges []porchv1alpha2.UpstreamDependent
	for _, name := range upstreamNames(pr) {
		if upstreamdependents.IdentityForPackageRevision(name) == upstreamKey {
			edges = append(edges, porchv1alpha2.UpstreamDependent{Name: pr.Name})
		}
	}
	for _, up := range pr.Status.SubpackageUpstreams {
		identity, _, ok := upstreamIdentity(up)
		if ok && identity == upstreamKey {
			edges = append(edges, porchv1alpha2.UpstreamDependent{Name: pr.Name, Path: up.Path})
		}
	}
	return edges
}

func containsEntry(edges []porchv1alpha2.UpstreamDependent, e porchv1alpha2.UpstreamDependent) bool {
	for _, edge := range edges {
		if edge.Name == e.Name && edge.Path == e.Path {
			return true
		}
	}
	return false
}

// equalSet reports whether a and b hold the same entries, in any order.
func equalSet(a, b []porchv1alpha2.UpstreamDependent) bool {
	if len(a) != len(b) {
		return false
	}
	for _, e := range a {
		if !containsEntry(b, e) {
			return false
		}
	}
	return true
}

// logRebuildRefused records why a truncated object stays truncated, so the reason is visible in the logs.
func logRebuildRefused(obj *porchv1alpha2.UpstreamDependents, reason string) {
	ctrl.Log.WithName("dependent-maintenance").Info("truncated upstream dependents stay truncated",
		"object", obj.Namespace+"/"+obj.Name, "reason", reason)
}

// save writes the kept entries, or deletes the object when it is left empty and not truncated. The
// status write carries the object's resourceVersion. The delete carries its UID and resourceVersion.
// Deleting an upstream object is the exception to decision D8, which says the controller never
// deletes them.
func (m *dependentMaintenance) save(ctx context.Context, obj *porchv1alpha2.UpstreamDependents,
	kept []porchv1alpha2.UpstreamDependent, stats *sweepStats) error {
	if len(kept) == 0 && !obj.Status.Truncated {
		rv := obj.ResourceVersion
		uid := obj.UID
		if err := m.client.Delete(ctx, obj, client.Preconditions{UID: &uid, ResourceVersion: &rv}); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("delete empty upstream dependents %s/%s: %w", obj.Namespace, obj.Name, err)
		}
		stats.deletedObjects++
		return nil
	}
	obj.Status.Dependents = kept
	obj.Status.DependentCount = len(kept)
	markWritten(obj)
	if err := m.client.Status().Update(ctx, obj); err != nil {
		return fmt.Errorf("update upstream dependents %s/%s: %w", obj.Namespace, obj.Name, err)
	}
	return nil
}

// clearCapCondition removes the cap condition and keeps any others.
func clearCapCondition(obj *porchv1alpha2.UpstreamDependents) {
	kept := obj.Status.Conditions[:0]
	for _, c := range obj.Status.Conditions {
		if c.Type != conditionCapReached {
			kept = append(kept, c)
		}
	}
	obj.Status.Conditions = kept
}

// waitTick waits for the next pacing tick, or returns ctx's error when cancelled.
func waitTick(ctx context.Context, pace *time.Ticker) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pace.C:
		return nil
	}
}
