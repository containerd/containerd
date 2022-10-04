package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
	"github.com/hashicorp/go-multierror"
	"github.com/montanaflynn/stats"
)

var (
	address        = "/run/containerd/containerd.sock"
	testImage      = "ghcr.io/containerd/busybox:1.28"
	testNamespace  = "testing"
	benchmarkFuncs = [...]func(*testing.B){BenchmarkRandom, BenchmarkGetImage, BenchmarkContainerCreate}
)

// https://stackoverflow.com/a/70535822/13675
func GetFunctionName(function interface{}) string {
	strs := strings.Split((runtime.FuncForPC(reflect.ValueOf(function).Pointer()).Name()), ".")
	return strs[len(strs)-1]
}

func main() {
	commit := os.Args[1]
	testNum := 100 // less than 4 can produce fatal errors in the stats calculations
	testing.Init()
	// disable the automatic iteration count system
	flag.Set("test.benchtime", "1x")
	flag.Parse()

	commitResults := BenchmarkCommitResults{
		Commit:  commit,
		Results: make(map[string]BenchmarkResult)}

	for _, benchmarkFunc := range benchmarkFuncs {
		benchmarkResult := BenchmarkResult{
			TestName:      GetFunctionName(benchmarkFunc),
			NumberOfTests: testNum,
			testFunction:  benchmarkFunc}

		for i := 0; i < benchmarkResult.NumberOfTests; i++ {
			// fmt.Printf("TestName: %s\n", benchmarkResult.TestName)
			res := testing.Benchmark(benchmarkResult.testFunction)
			// fmt.Printf("%d: %f\n", i, res.T.Seconds())
			benchmarkResult.addResult(res)
		}

		err := benchmarkResult.updateStats()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error when updating stats: %s\n", err)
		}

		commitResults.Results[benchmarkResult.TestName] = benchmarkResult
	}

	json, _ := json.MarshalIndent(commitResults, "", " ")
	fmt.Print(string(json[:]))
}

type BenchmarkCommitResults struct {
	Commit  string                     `json:"commit"`
	Results map[string]BenchmarkResult `json:"benchmarkResults"`
}

type BenchmarkResult struct {
	TestName      string `json:"testName"`
	NumberOfTests int    `json:"numberOfTests"`
	testFunction  func(*testing.B)
	TestsRun      int       `json:"testsRun"`
	testTimes     []float64 `json:"-"`
	StdDev        float64   `json:"stdDev"`
	Mean          float64   `json:"mean"`
	Min           float64   `json:"min"`
	Pct25         float64   `json:"pct25"`
	Pct50         float64   `json:"pct50"`
	Pct75         float64   `json:"pct75"`
	Pct90         float64   `json:"pct90"`
	Max           float64   `json:"max"`
}

// add a single result to testTimes
func (driver *BenchmarkResult) addResult(individualResult testing.BenchmarkResult) {
	//TODO add assert for single test
	driver.TestsRun = driver.TestsRun + individualResult.N
	driver.testTimes = append(driver.testTimes, individualResult.T.Seconds())
}

// calculate statistical metrics for all testTimes
func (results *BenchmarkResult) updateStats() error {
	//TODO add assert for single test
	var err, errs error
	results.StdDev, err = stats.StandardDeviation(results.testTimes)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Mean, err = stats.Mean(results.testTimes)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Min, err = stats.Min(results.testTimes)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Pct25, err = stats.Percentile(results.testTimes, 25)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Pct50, err = stats.Percentile(results.testTimes, 50)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Pct75, err = stats.Percentile(results.testTimes, 75)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Pct90, err = stats.Percentile(results.testTimes, 90)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	results.Max, err = stats.Max(results.testTimes)
	if err != nil {
		errs = multierror.Append(errs, err)
	}
	return errs
}

func BenchmarkRandom(b *testing.B) {
	for i := 0; i < b.N; i++ {
		rand.Seed(time.Now().UnixNano())
		sleepTime := time.Duration(rand.Int63n(50))
		// fmt.Printf("SleepTime = %d\n", sleepTime)
		time.Sleep(sleepTime * time.Second / 100)
	}
}

// This is a bad test. client.Fetch runtime depends on network speed.
func BenchmarkGetImage(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
		b.Fatalf("Error new client : %s\n", err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

	_, fetchErr := client.Fetch(ctx, testImage, getRemoteOpts()...)
	if fetchErr != nil {
		b.Fatalf("Error Fetch : %s\n", fetchErr)
	}

	_, getErr := client.GetImage(ctx, testImage)
	if getErr != nil {
		b.Fatalf("Error get Image : %s\n", getErr)
	}
}

func BenchmarkContainerCreate(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
		b.Fatalf("Error new client : %s\n", err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

	image, pullErr := client.Pull(ctx, testImage, WithPullUnpack)
	if pullErr != nil {
		b.Fatalf("Error Pull : %s\n", pullErr)
	}

	spec, err := oci.GenerateSpec(ctx, client, &containers.Container{ID: "test"}, oci.WithRootFSPath("/var/lib/containerd-test"), oci.WithImageConfig(image), withTrue())
	if err != nil {
		b.Fatalf("Error Generate Spec : %s\n", err)
	}

	var containers []Container
	defer func() {
		for _, c := range containers {
			if err := c.Delete(ctx, WithSnapshotCleanup); err != nil {
				b.Errorf("Error Container delete : %s\n", err)
			}
		}
	}()

	// reset the timer so that only the container creation time is counted
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("%s-%d", "test", i)
		container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithSpec(spec))
		if err != nil {
			b.Errorf("Error New Container : %s\n", err)
		} else {
			containers = append(containers, container)
		}
	}
}

func newClient(t testing.TB, address string, opts ...ClientOpt) (*Client, error) {
	if testing.Short() {
		t.Skip()
	}
	if rt := os.Getenv("TEST_RUNTIME"); rt != "" {
		opts = append(opts, WithDefaultRuntime(rt))
	}
	// testutil.RequiresRoot(t) is not needed here (already called in TestMain)
	return New(address, opts...)
}

func testContext(t testing.TB) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, testNamespace)
	if t != nil {
		ctx = logtest.WithT(ctx, t)
	}
	return ctx, cancel
}

func withTrue() oci.SpecOpts {
	return oci.WithProcessArgs("true")
}

func getRemoteOpts() []RemoteOpt {
	platformList := []string{"linux/amd64"}
	m := make(map[string]platforms.Matcher)
	var opts []RemoteOpt

	for _, platform := range platformList {
		p, err := platforms.Parse(platform)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error in getting RemoteOpts : %s\n", err)
		}
		m[platform] = platforms.NewMatcher(p)
		opts = append(opts, WithPlatform(platform))
	}
	return opts
}
