package performance

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	controlRepoName    = "iterative-control"
	controlPackageName = "control-package"
	testRepoName       = "iterative-test"
	testPackageName    = "test-package"

	startingWS = "v1"
)

var (
	repoCount   = flag.Int("repos", 1, "Number of repositories to create")
	iterations  = flag.String("iterations", "10,25,50", "Number of control packages to create PER REPOSITORY for each iteration (e.g. \"10,25,50,100\")")
	sampling    = flag.Int("sampling", 5, "Number of measurements to take per iteration")
	writeToFile = flag.Bool("write-to-file", false, "Write results to file")
	outputDir   = flag.String("output-dir", ".build/perf/iterative", "Where to write the results to if '-write-to-file' is set")
	printToLog  = flag.Bool("print-to-log", false, "Print results to log")
	cooldown    = flag.Duration("cooldown", 0, "Time to wait between iterations")
)

type IterativeTest struct {
	PerformanceSuite

	// entire test vars
	metrics    []FullMetricsData
	iterations []int
	sampling   int
	repos      []string
	cooldown   time.Duration

	// per subtest vars
	controlCount   int
	iterationIndex int
}

func TestIterative(t *testing.T) {
	if os.Getenv("PERF") != "1" {
		t.Skip("PERF != 1")
	}
	test := &IterativeTest{}
	test.UseGitea = true
	suite.Run(t, test)
}

func (t *IterativeTest) SetupSuite() {
	t.parseFlags() // validate flags before starting the git server
	t.PerformanceSuite.SetupSuite()
	t.createRepos()
	t.metrics = []FullMetricsData{}
}

func (t *IterativeTest) parseFlags() {
	flag.Parse()
	its, err := toIntSlice(*iterations)
	if err != nil {
		t.Fatalf("failed to parse iterations: %v", err)
	}
	if len(its) == 0 {
		t.Fatalf("Must specify at least one iteration (-iterations)")
	}
	t.iterations = its

	if *repoCount < 1 {
		t.Fatalf("Repository count must be at least 1 (-repos)")
	}
	t.repos = []string{}
	for i := range *repoCount {
		t.repos = append(t.repos, fmt.Sprintf("%s-%d", controlRepoName, i+1))
	}

	t.sampling = *sampling
	if t.sampling < 1 {
		t.Fatalf("Test package count must be at least 1 (-sampling)")
	}

	t.cooldown = *cooldown
}

func toIntSlice(s string) ([]int, error) {
	regex := regexp.MustCompile(`^\d+(,\d+)*$`)
	if !regex.MatchString(s) {
		return nil, fmt.Errorf("cannot parse %q as int slice", s)
	}
	var out []int
	for _, v := range strings.Split(s, ",") {
		i, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

func (t *IterativeTest) createRepos() {
	var wg sync.WaitGroup
	for _, repo := range t.repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, repo, suiteutils.GiteaUser, suiteutils.GiteaPassword)
		}()
	}
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), testRepoName, "iterativedir", suiteutils.GiteaUser, suiteutils.GiteaPassword)
	wg.Wait()
}

func (t *IterativeTest) SetupSubTest() {
	times := make([]MyDuration, len(t.repos))
	var wg sync.WaitGroup
	for i, repo := range t.repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			times[i] = Measure(func() { t.createPackageRevisions(t.controlCount, repo) })
		}()
	}
	wg.Wait()
	var timeToCreateAllRevisions MyDuration = 0
	for _, v := range times {
		timeToCreateAllRevisions += v
	}
	t.metrics = append(t.metrics, FullMetricsData{
		ControlRevisionCount:        t.controlCount,
		IterationIndex:              t.iterationIndex,
		CreateControlRevisionsTotal: timeToCreateAllRevisions,
		CreateControlRevisionsAvg:   AvgDuration(timeToCreateAllRevisions, t.controlCount),
	})
}

func (t *IterativeTest) TearDownSubTest() {
	timeToDeleteAllRevisions := Measure(func() { t.deletePackageRevisions() })
	t.metrics[len(t.metrics)-1].DeleteControlRevisionsTotal = timeToDeleteAllRevisions
	t.metrics[len(t.metrics)-1].DeleteControlRevisionsAvg = AvgDuration(timeToDeleteAllRevisions, t.controlCount)
}

func (t *IterativeTest) TestIterative() {
	for i, n := range t.iterations {
		t.controlCount = n
		t.iterationIndex = i + 1
		subtestName := fmt.Sprintf("Iterative-%d-%d", t.iterationIndex, t.controlCount)
		result := t.Run(subtestName, func() {
			var iterationMetrics []IterationMetricsData
			for range t.sampling {
				currentMetrics := t.collectMetrics()
				t.ensureTestPackagesDeleted()
				iterationMetrics = append(iterationMetrics, *currentMetrics)
			}
			t.mergeMetrics(iterationMetrics)
		})
		if !result {
			t.Errorf("%s failed, stopping early", subtestName)
			break
		}
		if *writeToFile {
			t.writeResult(&t.metrics[len(t.metrics)-1])
		}
		if i != len(t.iterations)-1 {
			<-time.After(t.cooldown)
		}
	}
	if *printToLog {
		t.printResults()
	}
	if *writeToFile {
		t.writeResults()
	}
}

func (t *IterativeTest) ensureTestPackagesDeleted() {
	list := &porchapi.PackageRevisionList{}
	t.ListE(list, client.InNamespace(t.Namespace), client.MatchingFields{"spec.packageName": testPackageName})
	for _, pr := range list.Items {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		t.UpdateApprovalL(&pr)
		t.DeleteL(&pr)
	}
}

// mergeMetrics averages the gathered metrics of a single iteration,
// then appends these to the overall results
func (t *IterativeTest) mergeMetrics(iterationMetrics []IterationMetricsData) {
	summed := iterationMetrics[0]
	length := len(iterationMetrics)
	for _, metric := range iterationMetrics[1:] {
		summed.List += metric.List
		summed.Create += metric.Create
		summed.UpdateResources += metric.UpdateResources
		summed.GetAfterResourceUpdate += metric.GetAfterResourceUpdate
		summed.Propose += metric.Propose
		summed.GetAfterPropose += metric.GetAfterPropose
		summed.Approve += metric.Approve
		summed.GetAfterPublish += metric.GetAfterPublish
		summed.DeleteProposed += metric.DeleteProposed
		summed.GetAfterProposeDelete += metric.GetAfterProposeDelete
		summed.Delete += metric.Delete
	}
	summed.List = AvgDuration(summed.List, length)
	summed.Create = AvgDuration(summed.Create, length)
	summed.UpdateResources = AvgDuration(summed.UpdateResources, length)
	summed.GetAfterResourceUpdate = AvgDuration(summed.GetAfterResourceUpdate, length)
	summed.Propose = AvgDuration(summed.Propose, length)
	summed.GetAfterPropose = AvgDuration(summed.GetAfterPropose, length)
	summed.Approve = AvgDuration(summed.Approve, length)
	summed.GetAfterPublish = AvgDuration(summed.GetAfterPublish, length)
	summed.DeleteProposed = AvgDuration(summed.DeleteProposed, length)
	summed.GetAfterProposeDelete = AvgDuration(summed.GetAfterProposeDelete, length)
	summed.Delete = AvgDuration(summed.Delete, length)
	t.metrics[len(t.metrics)-1].IterationMetricsData = summed
}

// collectMetrics runs the inner part of the test, collecting the specified metrics
func (t *IterativeTest) collectMetrics() *IterationMetricsData {
	output := &IterationMetricsData{}
	var pr *porchapi.PackageRevision

	output.List = Measure(func() { t.ListF(&porchapi.PackageRevisionList{}) })

	output.Create = Measure(func() { pr = t.CreatePackageDraftF(testRepoName, testPackageName, startingWS) })
	t.T().Cleanup(func() { t.DeleteL(pr) })

	resources := &porchapi.PackageRevisionResources{}
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, resources)
	resources.Spec.Resources["README.md"] = "# updated readme"
	output.UpdateResources = Measure(func() { t.UpdateF(resources) })

	output.GetAfterResourceUpdate = Measure(func() { t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, pr) })

	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	output.Propose = Measure(func() { t.UpdateF(pr) })

	output.GetAfterPropose = Measure(func() { t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, pr) })

	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	output.Approve = Measure(func() { t.UpdateApprovalF(pr) })

	output.GetAfterPublish = Measure(func() { t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, pr) })

	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	output.DeleteProposed = Measure(func() { t.UpdateApprovalF(pr) })

	output.GetAfterProposeDelete = Measure(func() { t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, pr) })

	output.Delete = Measure(func() { t.DeleteF(pr) })

	return output
}

// createPackageRevisions creates and publishes n number of control PackageRevisions.
// These will all be revisions of the same Package
func (t *IterativeTest) createPackageRevisions(n int, repoName string) {
	pr := t.CreatePackageDraftF(repoName, controlPackageName, startingWS)
	t.publishPackageRevision(pr)

	for i := 2; i <= n; i++ {
		pr = t.createNewPackageRevisionFrom(pr, repoName, i)
		t.publishPackageRevision(pr)
	}
}

// deletePackageRevisions deletes all PackageRevisions in the test cluster,
// including proposing deletion.
func (t *IterativeTest) deletePackageRevisions() {
	var wg sync.WaitGroup
	for _, repo := range append(t.repos, testRepoName) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prs := &porchapi.PackageRevisionList{}
			t.ListE(prs, client.MatchingFields{"spec.repository": repo})
			for _, pr := range prs.Items {
				pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
				t.UpdateApprovalL(&pr)
				t.DeleteL(&pr)
			}
		}()
	}
	wg.Wait()
}

// createNewPackageRevisionFrom takes a PackageRevision and creates a new PackageRevision from it
func (t *IterativeTest) createNewPackageRevisionFrom(pr *porchapi.PackageRevision, repoName string, i int) *porchapi.PackageRevision {
	newPr := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pr.Namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    pr.Spec.PackageName,
			WorkspaceName:  fmt.Sprintf("v%d", i),
			RepositoryName: repoName,
			Tasks:          pr.Spec.Tasks,
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}

	t.CreateF(newPr)
	return newPr
}

// publishPackageRevision proposes, then publishes an already existing draft PackageRevision
func (t *IterativeTest) publishPackageRevision(pr *porchapi.PackageRevision) {
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)

	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(pr)
}

// printResults prints all the gathered metrics to the log
func (t *IterativeTest) printResults() {
	for _, metrics := range t.metrics {
		t.Logf("results for %d revisions:\n%+v", metrics.ControlRevisionCount, metrics)
	}
}

// writeResult writes the result of one iteration to a json file
func (t *IterativeTest) writeResult(data *FullMetricsData) {
	if err := t.ensureOutputDir(); err != nil {
		return
	}
	filename := fmt.Sprintf("%d-%d.json", data.IterationIndex, data.ControlRevisionCount)
	filepth := filepath.Join(*outputDir, filename)
	t.write(filepth, data)
}

// writeResults writes the results of all iteration to a combined json file
func (t *IterativeTest) writeResults() {
	if err := t.ensureOutputDir(); err != nil {
		return
	}
	filepth := filepath.Join(*outputDir, "full.json")
	t.write(filepth, t.metrics)
}

func (t *IterativeTest) write(filename string, data any) {
	file, err := os.Create(filename)
	if err != nil {
		t.Logf("unable to create %s: %v", filename, err)
		return
	}
	marshalled, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Logf("failed to marshal metrics: %v", err)
		return
	}
	if _, err = file.Write(marshalled); err != nil {
		t.Logf("failed to write to %s: %v", filename, err)
	}
}

func (t *IterativeTest) ensureOutputDir() error {
	if err := os.MkdirAll(*outputDir, 0775); err != nil {
		t.Logf("unable to create %s: %v", *outputDir, err)
		return err
	}
	return nil
}
