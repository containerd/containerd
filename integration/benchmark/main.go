package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
        "golang.org/x/perf/internal/stats"
	"testing"
	"time"
)

func main() {
	commit := os.Args[1]
	testNum := 10
	fmt.Println(commit)
	testing.Init()
	flag.Set("test.benchtime", "1x")
	flag.Parse()
	resultStats := BenchmarkTestDriver{
		testName:      "BenchmarkExample",
		numberOfTests: testNum,
		testFunction:  BenchmarkExample}
	for i := 0; i < resultStats.numberOfTests; i++ {
		res := testing.Benchmark(resultStats.testFunction)
		fmt.Printf("Number of run: %d, time taken: %d\n", res.N, res.T)
		resultStats.updateResult(res)
	}
	fmt.Printf("Driver tests run: %d", resultStats.testsRun)
}

type BenchmarkTestDriver struct {
	testName      string `json:"testName"`
	numberOfTests int    `json:"numberOfTests"`
	testFunction  func(*testing.B)
	testsRun      int
        testTime      []float64
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
	driver.testsRun = driver.testsRun + individualResult.N
        append(individualResult.T, driver.testTime)
}
