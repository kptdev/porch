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

package crd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Report-only: counts the API writes one package makes through v1 (clone) and v2 (upgrade), per step,
// object and field manager. Exact on a single-replica control plane (kind). Run alone:
//
//	E2E=1 E2E_WRITE_COUNTS=1 go test ./test/e2e/crd -ginkgo.label-filter=writecount
var _ = Describe("Write counts", Ordered, Label("writecount"), func() {
	const settle = 3 * time.Second

	var (
		env        *testEnv
		kubeClient *kubernetes.Clientset
		rec        *writeRecorder
		report     strings.Builder
	)

	BeforeAll(func() {
		if os.Getenv("E2E_WRITE_COUNTS") == "" {
			Skip("set E2E_WRITE_COUNTS to run")
		}
		env = sharedEnv()
		var err error
		kubeClient, err = kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		triggerRepoSync(env.Ctx, env.Namespace, testBlueprintsRepo)
		waitForDiscovery(env.Ctx, env.Namespace, crdName(testBlueprintsRepo, "basens", "v2"))
		rec = startWriteRecorder(env.Ctx, env.Namespace)
		DeferCleanup(rec.stop)
	})

	AfterAll(func() {
		out := os.Getenv("E2E_WRITE_COUNTS_OUT")
		if out == "" {
			out = "/tmp/porch-write-counts.txt"
		}
		GinkgoWriter.Print(report.String())
		Expect(os.WriteFile(out, []byte(report.String()), 0o600)).To(Succeed())
		GinkgoWriter.Printf("write counts written to %s\n", out)
	})

	// step runs action, waits for the controller to go quiet and records what was written.
	step := func(name string, action func()) {
		before := scrape(env.Ctx, kubeClient)
		rec.setStep(name)
		action()
		time.Sleep(settle)
		report.WriteString(formatStep(name, before, scrape(env.Ctx, kubeClient), rec.take(name)))
	}

	It("records writes for clone (v1) and upgrade (v2)", func() {
		upstreamV1 := crdName(testBlueprintsRepo, "basens", "v1")
		upstreamV2 := crdName(testBlueprintsRepo, "basens", "v2")
		v1 := newPackageRevision(env.Namespace, porchTestRepo, "wc-pkg", "v1", withCloneFromRef(upstreamV1))
		v2 := newPackageRevision(env.Namespace, porchTestRepo, "wc-pkg", "v2", withUpgrade(upstreamV1, upstreamV2, v1.Name))
		cm := map[string]string{"wc.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: wc\ndata:\n  k: v\n"}

		for _, rev := range []struct {
			pr      *porchv1alpha2.PackageRevision
			version string
		}{{v1, "v1"}, {v2, "v2"}} {
			pr := rev.pr
			step(rev.version+" create", func() {
				Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
				waitForReady(env.Ctx, pr)
				waitForRendered(env.Ctx, pr)
			})
			step(rev.version+" push", func() {
				updatePRRResources(env.Ctx, env.Namespace, pr.Name, cm)
				waitForRendered(env.Ctx, pr)
			})
			step(rev.version+" propose", func() {
				patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
				waitForReady(env.Ctx, pr)
			})
			step(rev.version+" approve", func() {
				patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
				waitForPublished(env.Ctx, pr)
				waitForReady(env.Ctx, pr)
			})
		}

		main := crdName(porchTestRepo, "wc-pkg", "main")
		report.WriteString(formatOwnership(env.Ctx, env.Namespace, []string{v1.Name, v2.Name, main}))
		report.WriteString(rec.totals())
	})
})

// --- metrics scraping ---

var metricLine = regexp.MustCompile(`^(\w+)\{(.*)\} ([0-9.e+-]+)$`)

type metricSet map[string]float64

func parseMetrics(text string, keep func(name, labels string) bool, prefix string, into metricSet) {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for sc.Scan() {
		m := metricLine.FindStringSubmatch(sc.Text())
		if m == nil || !keep(m[1], m[2]) {
			continue
		}
		v, err := strconv.ParseFloat(m[3], 64)
		if err == nil {
			into[prefix+m[1]+"{"+m[2]+"}"] += v
		}
	}
}

// scrape reads API server and controller counters relevant to PackageRevision writes.
func scrape(ctx context.Context, kc *kubernetes.Clientset) metricSet {
	out := metricSet{}
	raw, err := kc.RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
	Expect(err).NotTo(HaveOccurred())
	parseMetrics(string(raw), func(name, labels string) bool {
		return (name == "apiserver_request_total" || name == "etcd_request_duration_seconds_count") &&
			strings.Contains(labels, `resource="packagerevisions"`) && strings.Contains(labels, `group="porch.kpt.dev"`)
	}, "", out)

	var pods corev1.PodList
	Expect(k8sClient.List(ctx, &pods, client.InNamespace("porch-system"))).To(Succeed())
	for i := range pods.Items {
		p := &pods.Items[i]
		if !strings.HasPrefix(p.Name, "porch-controllers") || p.Status.Phase != corev1.PodRunning {
			continue
		}
		body, err := kc.CoreV1().Pods("porch-system").ProxyGet("", p.Name, "9464", "metrics", nil).DoRaw(ctx)
		if err != nil {
			continue
		}
		parseMetrics(string(body), func(name, labels string) bool {
			return name == "rest_client_requests_total" ||
				(name == "controller_runtime_reconcile_total" && strings.Contains(labels, `controller="packagerevision"`))
		}, "ctrl:", out)
	}
	return out
}

var labelRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

func labelValue(series, label string) string {
	for _, m := range labelRe.FindAllStringSubmatch(series, -1) {
		if m[1] == label {
			return m[2]
		}
	}
	return ""
}

// deltas groups the positive differences b-a by the given labels of each series name.
func deltas(a, b metricSet, name string, labels ...string) map[string]float64 {
	out := map[string]float64{}
	for k, v := range b {
		if !strings.HasPrefix(k, name+"{") {
			continue
		}
		if d := v - a[k]; d > 0 {
			parts := make([]string, 0, len(labels))
			for _, l := range labels {
				if val := labelValue(k, l); val != "" {
					parts = append(parts, val)
				}
			}
			out[strings.Join(parts, "/")] += d
		}
	}
	return out
}

func formatCounts(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.0f", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func formatStep(name string, a, b metricSet, writes map[string]int) string {
	var s strings.Builder
	fmt.Fprintf(&s, "== %s\n", name)
	fmt.Fprintf(&s, "  api requests (verb/subresource): %s\n", formatCounts(deltas(a, b, "apiserver_request_total", "verb", "subresource")))
	fmt.Fprintf(&s, "  etcd ops (operation):            %s\n", formatCounts(deltas(a, b, "etcd_request_duration_seconds_count", "operation")))
	fmt.Fprintf(&s, "  controller client (method):      %s\n", formatCounts(deltas(a, b, "ctrl:rest_client_requests_total", "method")))
	fmt.Fprintf(&s, "  controller reconciles (result):  %s\n", formatCounts(deltas(a, b, "ctrl:controller_runtime_reconcile_total", "result")))
	keys := make([]string, 0, len(writes))
	for k := range writes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&s, "  watch write  %-70s %d\n", k, writes[k])
	}
	return s.String()
}

// --- per-object write attribution from a watch ---

// writeRecorder counts PackageRevision watch events per step, object and the field managers whose
// managedFields timestamp moved (a manager's time only changes when its write changed something).
type writeRecorder struct {
	mu      sync.Mutex
	step    string
	counts  map[string]map[string]int
	all     map[string]int
	last    map[string]map[string]metav1.Time
	watcher watch.Interface
}

func startWriteRecorder(ctx context.Context, namespace string) *writeRecorder {
	wc, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	w, err := wc.Watch(ctx, &porchv1alpha2.PackageRevisionList{}, client.InNamespace(namespace))
	Expect(err).NotTo(HaveOccurred())
	r := &writeRecorder{counts: map[string]map[string]int{}, all: map[string]int{}, last: map[string]map[string]metav1.Time{}, watcher: w}
	go func() {
		for ev := range w.ResultChan() {
			pr, ok := ev.Object.(*porchv1alpha2.PackageRevision)
			if !ok || !strings.Contains(pr.Name, "wc-pkg") {
				continue
			}
			r.observe(ev.Type, pr)
		}
	}()
	return r
}

func (r *writeRecorder) observe(t watch.EventType, pr *porchv1alpha2.PackageRevision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := map[string]metav1.Time{}
	var changed []string
	for _, mf := range pr.ManagedFields {
		k := mf.Manager + "|" + string(mf.Operation) + "|" + mf.Subresource
		if mf.Time != nil {
			now[k] = *mf.Time
		}
		prev, seen := r.last[pr.Name][k]
		if !seen || (mf.Time != nil && !prev.Equal(mf.Time)) {
			changed = append(changed, k)
		}
	}
	r.last[pr.Name] = now
	if r.step == "" || t == watch.Bookmark {
		return
	}
	if len(changed) == 0 {
		changed = []string{"(same-second, manager not identified)"}
	}
	sort.Strings(changed)
	key := fmt.Sprintf("%s %s %s", t, shortPRName(pr.Name), strings.Join(changed, ","))
	if r.counts[r.step] == nil {
		r.counts[r.step] = map[string]int{}
	}
	r.counts[r.step][key]++
	r.all[fmt.Sprintf("%s %s", t, shortPRName(pr.Name))]++
}

func shortPRName(name string) string {
	return strings.TrimPrefix(name, porchTestRepo+".")
}

func (r *writeRecorder) setStep(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.step = s
}

// take returns a copy of a step's counts; late watch events keep updating the live map.
func (r *writeRecorder) take(s string) map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.counts[s])
}

func (r *writeRecorder) totals() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var s strings.Builder
	s.WriteString("== watch totals (event object)\n")
	keys := make([]string, 0, len(r.all))
	total := 0
	for k, v := range r.all {
		keys = append(keys, k)
		total += v
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&s, "  %-40s %d\n", k, r.all[k])
	}
	fmt.Fprintf(&s, "  total %d writes for 2 package-phases\n", total)
	return s.String()
}

func (r *writeRecorder) stop() { r.watcher.Stop() }

// --- final ownership and conditions ---

func formatOwnership(ctx context.Context, namespace string, names []string) string {
	var s strings.Builder
	s.WriteString("== final field ownership (manager|operation|subresource: top-level fields)\n")
	for _, name := range names {
		pr := &porchv1alpha2.PackageRevision{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, pr); err != nil {
			fmt.Fprintf(&s, "  %s: %v\n", shortPRName(name), err)
			continue
		}
		fmt.Fprintf(&s, "  %s (generation %d, lifecycle %s)\n", shortPRName(name), pr.Generation, pr.Spec.Lifecycle)
		for _, mf := range pr.ManagedFields {
			fmt.Fprintf(&s, "    %s|%s|%s: %s\n", mf.Manager, mf.Operation, mf.Subresource, fieldsSummary(mf.FieldsV1))
		}
		for _, c := range pr.Status.Conditions {
			fmt.Fprintf(&s, "    condition %s=%s reason=%s observedGeneration=%d lastTransitionTime=%s\n",
				c.Type, c.Status, c.Reason, c.ObservedGeneration, c.LastTransitionTime.UTC().Format(time.RFC3339))
		}
	}
	return s.String()
}

var fieldKeyRe = regexp.MustCompile(`"(f:[^"]+|k:[^"]+)":`)

// fieldsSummary lists the owned field keys two levels deep (e.g. f:status.f:conditions).
func fieldsSummary(f *metav1.FieldsV1) string {
	if f == nil {
		return ""
	}
	var top map[string]map[string]any
	if err := json.Unmarshal(f.Raw, &top); err != nil {
		return strings.Join(fieldKeyRe.FindAllString(string(f.Raw), 8), " ")
	}
	var parts []string
	for k, v := range top {
		if k == "." {
			continue
		}
		var sub []string
		for sk := range v {
			if sk != "." {
				sub = append(sub, sk)
			}
		}
		sort.Strings(sub)
		parts = append(parts, k+"{"+strings.Join(sub, ",")+"}")
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}
