package gc

import "github.com/docker/go-metrics"

var (
	// GcCounter metrics for garbage collection
	GCCounter metrics.LabeledCounter

	GCProcessTimer metrics.Timer
)

func init() {
	ns := metrics.NewNamespace("containerd", "gc", nil)
	GCCounter = ns.NewLabeledCounter("gc_count", "run time of garbage collection", "status")
	GCProcessTimer = ns.NewTimer("gc_process", "time spend for garbage collection")
	metrics.Register(ns)
}
