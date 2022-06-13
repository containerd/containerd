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
	fmt.Printf("Number of run: %d \n", res.N)
}

func BenchmarkExample(b *testing.B) {
    for i := 0; i < b.N; i++ {
	sleepTime := time.Duration(rand.Int63n(24))
	time.Sleep(sleepTime * 0 * time.Second)
    }
}

