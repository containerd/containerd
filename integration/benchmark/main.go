package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
	"github.com/montanaflynn/stats"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"
	"time"
)

var (
	address = "/run/containerd/containerd.sock"
	testImage = "ghcr.io/containerd/busybox:1.32"
	testNamespace     = "testing"
    )

func main() {
	commit := os.Args[1]
	testNum := 100
	testing.Init()
	flag.Set("test.benchtime", "1x")
	flag.Parse()
	resultStats := BenchmarkTestDriver{
		TestName:      "BenchmarkExample",
		NumberOfTests: testNum,
		testFunction: BenchmarkGetImage}
	for i := 0; i < resultStats.NumberOfTests; i++ {
		res := testing.Benchmark(resultStats.testFunction)
                // fmt.Printf("%d: %f\n", i, res.T.Seconds())
		resultStats.updateResult(res)
	}
        
        tests := make([]BenchmarkTestDriver, 0)
        tests = append(tests, resultStats) 
        commitResult := BenchmarkCommitResult{
            Commit: commit,
            Results: tests}
        resultHistory := make([]BenchmarkCommitResult, 0) 
        resultHistory = append(resultHistory, commitResult) 
        history := BenchmarkResultHistory{Commits: resultHistory}
	json, _ := json.MarshalIndent(history, "", " ")
	err := ioutil.WriteFile("output.json", json, 0644)
	if err != nil {
		fmt.Printf("WriteFile Error: %v\n", err)
	}
}

type BenchmarkResultHistory struct {
    Commits []BenchmarkCommitResult `json:"commitHistory"`
}

type BenchmarkCommitResult struct {
    Commit string `json:"commit"`
    Results []BenchmarkTestDriver `json:"benchmarkTests"`
}

type BenchmarkTestDriver struct {
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

func BenchmarkExample(b *testing.B) {
	for i := 0; i < b.N; i++ {
		rand.Seed(time.Now().UnixNano())
		sleepTime := time.Duration(rand.Int63n(2))
		fmt.Printf("SleepTime = %d\n", sleepTime)
		time.Sleep(sleepTime * time.Second)
	}
}

func (driver *BenchmarkTestDriver) updateResult(individualResult testing.BenchmarkResult) {
	//TODO add assert for single test
	driver.TestsRun = driver.TestsRun + individualResult.N
	driver.testTimes = append(driver.testTimes, individualResult.T.Seconds())
	driver.StdDev, _ = stats.StandardDeviation(driver.testTimes)
	driver.Mean, _ = stats.Mean(driver.testTimes)
	driver.Min, _ = stats.Min(driver.testTimes)
	driver.Pct25, _ = stats.Percentile(driver.testTimes, 25)
	driver.Pct50, _ = stats.Percentile(driver.testTimes, 50)
	driver.Pct75, _ = stats.Percentile(driver.testTimes, 75)
	driver.Pct90, _ = stats.Percentile(driver.testTimes, 90)
	driver.Max, _ = stats.Max(driver.testTimes)
}

func BenchmarkGetImage(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
                fmt.Printf("Error new client : %s\n", err)
		b.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

        _, fetchErr := client.Fetch(ctx, testImage, getRemoteOpts()...)
        if fetchErr != nil {
            fmt.Printf("Error Fetch : %s\n", fetchErr)
            return
        }
	
        _, getErr := client.GetImage(ctx, testImage)
	if getErr != nil {
                fmt.Printf("Error get Image : %s\n", getErr)
		b.Error(getErr)
		return
	}
}

func BenchmarkContainerCreate(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
                fmt.Printf("Error new client : %s\n", err)
		b.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

        newImage, newImageErr := client.Fetch(ctx, testImage, getRemoteOpts()...)
        if newImageErr != nil {
            fmt.Printf("Error Fetch : %s\n", newImageErr)
            return
        }
        fmt.Printf("Fetched Image : %s\n", newImage.Name)
	
        image, getImageErr := client.GetImage(ctx, testImage)
	if getImageErr != nil {
                fmt.Printf("Error get Image : %s\n", getImageErr)
		b.Error(err)
		return
	}
	spec, err := oci.GenerateSpec(ctx, client, &containers.Container{ID: b.Name()}, oci.WithRootFSPath("/var/lib/containerd-test"), oci.WithImageConfig(image), withTrue())
	if err != nil {
                fmt.Printf("Error Generate Spec : %s\n", err)
		b.Error(err)
		return
	}
	var containers []Container
	defer func() {
		for _, c := range containers {
			if err := c.Delete(ctx, WithSnapshotCleanup); err != nil {
                                fmt.Printf("Error Container delete : %s\n", err)
				b.Error(err)
			}
		}
	}()

	// reset the timer before creating containers
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("%s-%d", b.Name(), i)
		container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithSpec(spec))
		if err != nil {
                        fmt.Printf("Error New Container : %s\n", err)
			b.Error(err)
			return
		}
		containers = append(containers, container)
	}
	b.StopTimer()
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
	platformList := []string{"linux/amd64", "linux/arm64/v8", "linux/s390x"}
	m := make(map[string]platforms.Matcher)
	var opts []RemoteOpt

	for _, platform := range platformList {
		p, err := platforms.Parse(platform)
		if err != nil {
                    fmt.Printf("Error in getting RemoteOpts : %s\n", err)
		}
		m[platform] = platforms.NewMatcher(p)
		opts = append(opts, WithPlatform(platform))
	}
        return opts
}
