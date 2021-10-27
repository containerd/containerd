package format

import (
	"context"
	"errors"
	"runtime"

	cid "github.com/ipfs/go-cid"
)

// parallelBatchCommits is the number of batch commits that can be in-flight before blocking.
// TODO(ipfs/go-ipfs#4299): Experiment with multiple datastores, storage
// devices, and CPUs to find the right value/formula.
var parallelCommits = runtime.NumCPU()

// ErrNotCommited is returned when closing a batch that hasn't been successfully
// committed.
var ErrNotCommited = errors.New("error: batch not commited")

// ErrClosed is returned when operating on a batch that has already been closed.
var ErrClosed = errors.New("error: batch closed")

// NewBatch returns a node buffer (Batch) that buffers nodes internally and
// commits them to the underlying DAGService in batches. Use this if you intend
// to add or remove a lot of nodes all at once.
//
// If the passed context is canceled, any in-progress commits are aborted.
//
func NewBatch(ctx context.Context, na NodeAdder, opts ...BatchOption) *Batch {
	ctx, cancel := context.WithCancel(ctx)
	bopts := defaultBatchOptions
	for _, o := range opts {
		o(&bopts)
	}

	// Commit numCPU batches at once, but split the maximum buffer size over all commits in flight.
	bopts.maxSize /= parallelCommits
	bopts.maxNodes /= parallelCommits
	return &Batch{
		na:            na,
		ctx:           ctx,
		cancel:        cancel,
		commitResults: make(chan error, parallelCommits),
		opts:          bopts,
	}
}

// Batch is a buffer for batching adds to a dag.
type Batch struct {
	na NodeAdder

	ctx    context.Context
	cancel func()

	activeCommits int
	err           error
	commitResults chan error

	nodes []Node
	size  int

	opts batchOptions
}

func (t *Batch) processResults() {
	for t.activeCommits > 0 {
		select {
		case err := <-t.commitResults:
			t.activeCommits--
			if err != nil {
				t.setError(err)
				return
			}
		default:
			return
		}
	}
}

func (t *Batch) asyncCommit() {
	numBlocks := len(t.nodes)
	if numBlocks == 0 {
		return
	}
	if t.activeCommits >= parallelCommits {
		select {
		case err := <-t.commitResults:
			t.activeCommits--

			if err != nil {
				t.setError(err)
				return
			}
		case <-t.ctx.Done():
			t.setError(t.ctx.Err())
			return
		}
	}
	go func(ctx context.Context, b []Node, result chan error, na NodeAdder) {
		select {
		case result <- na.AddMany(ctx, b):
		case <-ctx.Done():
		}
	}(t.ctx, t.nodes, t.commitResults, t.na)

	t.activeCommits++
	t.nodes = make([]Node, 0, numBlocks)
	t.size = 0

	return
}

// Add adds a node to the batch and commits the batch if necessary.
func (t *Batch) Add(ctx context.Context, nd Node) error {
	return t.AddMany(ctx, []Node{nd})
}

// AddMany many calls Add for every given Node, thus batching and
// commiting them as needed.
func (t *Batch) AddMany(ctx context.Context, nodes []Node) error {
	if t.err != nil {
		return t.err
	}
	// Not strictly necessary but allows us to catch errors early.
	t.processResults()

	if t.err != nil {
		return t.err
	}

	t.nodes = append(t.nodes, nodes...)
	for _, nd := range nodes {
		t.size += len(nd.RawData())
	}

	if t.size > t.opts.maxSize || len(t.nodes) > t.opts.maxNodes {
		t.asyncCommit()
	}
	return t.err
}

// Commit commits batched nodes.
func (t *Batch) Commit() error {
	if t.err != nil {
		return t.err
	}

	t.asyncCommit()

loop:
	for t.activeCommits > 0 {
		select {
		case err := <-t.commitResults:
			t.activeCommits--
			if err != nil {
				t.setError(err)
				break loop
			}
		case <-t.ctx.Done():
			t.setError(t.ctx.Err())
			break loop
		}
	}

	return t.err
}

func (t *Batch) setError(err error) {
	t.err = err

	t.cancel()

	// Drain as much as we can without blocking.
loop:
	for {
		select {
		case <-t.commitResults:
		default:
			break loop
		}
	}

	// Be nice and cleanup. These can take a *lot* of memory.
	t.commitResults = nil
	t.na = nil
	t.ctx = nil
	t.nodes = nil
	t.size = 0
	t.activeCommits = 0
}

// BatchOption provides a way of setting internal options of
// a Batch.
//
// See this post about the "functional options" pattern:
// http://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis
type BatchOption func(o *batchOptions)

type batchOptions struct {
	maxSize  int
	maxNodes int
}

var defaultBatchOptions = batchOptions{
	maxSize: 8 << 20,

	// By default, only batch up to 128 nodes at a time.
	// The current implementation of flatfs opens this many file
	// descriptors at the same time for the optimized batch write.
	maxNodes: 128,
}

// MaxSizeBatchOption sets the maximum amount of buffered data before writing
// blocks.
func MaxSizeBatchOption(size int) BatchOption {
	return func(o *batchOptions) {
		o.maxSize = size
	}
}

// MaxNodesBatchOption sets the maximum number of buffered nodes before writing
// blocks.
func MaxNodesBatchOption(num int) BatchOption {
	return func(o *batchOptions) {
		o.maxNodes = num
	}
}

// BufferedDAG implements DAGService using a Batch NodeAdder to wrap add
// operations in the given DAGService. It will trigger Commit() before any
// non-Add operations, but otherwise calling Commit() is left to the user.
type BufferedDAG struct {
	ds DAGService
	b  *Batch
}

// NewBufferedDAG creates a BufferedDAG using the given DAGService and the
// given options for the Batch NodeAdder.
func NewBufferedDAG(ctx context.Context, ds DAGService, opts ...BatchOption) *BufferedDAG {
	return &BufferedDAG{
		ds: ds,
		b:  NewBatch(ctx, ds, opts...),
	}
}

// Commit calls commit on the Batch.
func (bd *BufferedDAG) Commit() error {
	return bd.b.Commit()
}

// Add adds a new node using Batch.
func (bd *BufferedDAG) Add(ctx context.Context, n Node) error {
	return bd.b.Add(ctx, n)
}

// AddMany adds many nodes using Batch.
func (bd *BufferedDAG) AddMany(ctx context.Context, nds []Node) error {
	return bd.b.AddMany(ctx, nds)
}

// Get commits and gets a node from the DAGService.
func (bd *BufferedDAG) Get(ctx context.Context, c cid.Cid) (Node, error) {
	err := bd.b.Commit()
	if err != nil {
		return nil, err
	}
	return bd.ds.Get(ctx, c)
}

// GetMany commits and gets nodes from the DAGService.
func (bd *BufferedDAG) GetMany(ctx context.Context, cs []cid.Cid) <-chan *NodeOption {
	err := bd.b.Commit()
	if err != nil {
		ch := make(chan *NodeOption, 1)
		defer close(ch)
		ch <- &NodeOption{
			Node: nil,
			Err:  err,
		}
		return ch
	}
	return bd.ds.GetMany(ctx, cs)
}

// Remove commits and removes a node from the DAGService.
func (bd *BufferedDAG) Remove(ctx context.Context, c cid.Cid) error {
	err := bd.b.Commit()
	if err != nil {
		return err
	}
	return bd.ds.Remove(ctx, c)
}

// RemoveMany commits and removes nodes from the DAGService.
func (bd *BufferedDAG) RemoveMany(ctx context.Context, cs []cid.Cid) error {
	err := bd.b.Commit()
	if err != nil {
		return err
	}
	return bd.ds.RemoveMany(ctx, cs)
}
