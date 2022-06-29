package main

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"
)

func main() {
	commit := os.Args[1]
	fmt.Println(commit)
	res := testing.Benchmark(BenchmarkExample)
        fmt.Printf("Number of run: %d, time taken: %d\n", res.N, res.T)
}

type benchresult struct {
	TestName      int `json:"testName"`
	numberOfTests int `json:"numberOfTests"`
}

func BenchmarkExample(b *testing.B) {
	for i := 0; i < b.N; i++ {
                rand.Seed(time.Now().UnixNano())
		sleepTime := time.Duration(rand.Int63n(3))
                fmt.Printf("SleepTime = %d\n", sleepTime)
		time.Sleep(sleepTime * time.Second)
	}
}
