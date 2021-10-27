// Package merkledag implements the IPFS Merkle DAG data structures.
package merkledag

import (
	"context"
	"fmt"
	"sync"

	blocks "github.com/ipfs/go-block-format"
	bserv "github.com/ipfs/go-blockservice"
	cid "github.com/ipfs/go-cid"
	ipldcbor "github.com/ipfs/go-ipld-cbor"
	format "github.com/ipfs/go-ipld-format"
	legacy "github.com/ipfs/go-ipld-legacy"
	dagpb "github.com/ipld/go-codec-dagpb"

	// blank import is used to register the IPLD raw codec
	_ "github.com/ipld/go-ipld-prime/codec/raw"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
)

// TODO: We should move these registrations elsewhere. Really, most of the IPLD
// functionality should go in a `go-ipld` repo but that will take a lot of work
// and design.
func init() {
	format.Register(cid.DagProtobuf, DecodeProtobufBlock)
	format.Register(cid.Raw, DecodeRawBlock)
	format.Register(cid.DagCBOR, ipldcbor.DecodeBlock)

	legacy.RegisterCodec(cid.DagProtobuf, dagpb.Type.PBNode, ProtoNodeConverter)
	legacy.RegisterCodec(cid.Raw, basicnode.Prototype.Bytes, RawNodeConverter)
}

// contextKey is a type to use as value for the ProgressTracker contexts.
type contextKey string

const progressContextKey contextKey = "progress"

// NewDAGService constructs a new DAGService (using the default implementation).
// Note that the default implementation is also an ipld.LinkGetter.
func NewDAGService(bs bserv.BlockService) *dagService {
	return &dagService{Blocks: bs}
}

// dagService is an IPFS Merkle DAG service.
// - the root is virtual (like a forest)
// - stores nodes' data in a BlockService
// TODO: should cache Nodes that are in memory, and be
//       able to free some of them when vm pressure is high
type dagService struct {
	Blocks bserv.BlockService
}

// Add adds a node to the dagService, storing the block in the BlockService
func (n *dagService) Add(ctx context.Context, nd format.Node) error {
	if n == nil { // FIXME remove this assertion. protect with constructor invariant
		return fmt.Errorf("dagService is nil")
	}

	return n.Blocks.AddBlock(nd)
}

func (n *dagService) AddMany(ctx context.Context, nds []format.Node) error {
	blks := make([]blocks.Block, len(nds))
	for i, nd := range nds {
		blks[i] = nd
	}
	return n.Blocks.AddBlocks(blks)
}

// Get retrieves a node from the dagService, fetching the block in the BlockService
func (n *dagService) Get(ctx context.Context, c cid.Cid) (format.Node, error) {
	if n == nil {
		return nil, fmt.Errorf("dagService is nil")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	b, err := n.Blocks.GetBlock(ctx, c)
	if err != nil {
		if err == bserv.ErrNotFound {
			return nil, format.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get block for %s: %v", c, err)
	}

	return legacy.DecodeNode(ctx, b)
}

// GetLinks return the links for the node, the node doesn't necessarily have
// to exist locally.
func (n *dagService) GetLinks(ctx context.Context, c cid.Cid) ([]*format.Link, error) {
	if c.Type() == cid.Raw {
		return nil, nil
	}
	node, err := n.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	return node.Links(), nil
}

func (n *dagService) Remove(ctx context.Context, c cid.Cid) error {
	return n.Blocks.DeleteBlock(c)
}

// RemoveMany removes multiple nodes from the DAG. It will likely be faster than
// removing them individually.
//
// This operation is not atomic. If it returns an error, some nodes may or may
// not have been removed.
func (n *dagService) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	// TODO(#4608): make this batch all the way down.
	for _, c := range cids {
		if err := n.Blocks.DeleteBlock(c); err != nil {
			return err
		}
	}
	return nil
}

// GetLinksDirect creates a function to get the links for a node, from
// the node, bypassing the LinkService.  If the node does not exist
// locally (and can not be retrieved) an error will be returned.
func GetLinksDirect(serv format.NodeGetter) GetLinks {
	return func(ctx context.Context, c cid.Cid) ([]*format.Link, error) {
		nd, err := serv.Get(ctx, c)
		if err != nil {
			if err == bserv.ErrNotFound {
				err = format.ErrNotFound
			}
			return nil, err
		}
		return nd.Links(), nil
	}
}

type sesGetter struct {
	bs *bserv.Session
}

// Get gets a single node from the DAG.
func (sg *sesGetter) Get(ctx context.Context, c cid.Cid) (format.Node, error) {
	blk, err := sg.bs.GetBlock(ctx, c)
	switch err {
	case bserv.ErrNotFound:
		return nil, format.ErrNotFound
	case nil:
		// noop
	default:
		return nil, err
	}

	return legacy.DecodeNode(ctx, blk)
}

// GetMany gets many nodes at once, batching the request if possible.
func (sg *sesGetter) GetMany(ctx context.Context, keys []cid.Cid) <-chan *format.NodeOption {
	return getNodesFromBG(ctx, sg.bs, keys)
}

// Session returns a NodeGetter using a new session for block fetches.
func (n *dagService) Session(ctx context.Context) format.NodeGetter {
	return &sesGetter{bserv.NewSession(ctx, n.Blocks)}
}

// FetchGraph fetches all nodes that are children of the given node
func FetchGraph(ctx context.Context, root cid.Cid, serv format.DAGService) error {
	return FetchGraphWithDepthLimit(ctx, root, -1, serv)
}

// FetchGraphWithDepthLimit fetches all nodes that are children to the given
// node down to the given depth. maxDepth=0 means "only fetch root",
// maxDepth=1 means "fetch root and its direct children" and so on...
// maxDepth=-1 means unlimited.
func FetchGraphWithDepthLimit(ctx context.Context, root cid.Cid, depthLim int, serv format.DAGService) error {
	var ng format.NodeGetter = NewSession(ctx, serv)

	set := make(map[cid.Cid]int)

	// Visit function returns true when:
	// * The element is not in the set and we're not over depthLim
	// * The element is in the set but recorded depth is deeper
	//   than currently seen (if we find it higher in the tree we'll need
	//   to explore deeper than before).
	// depthLim = -1 means we only return true if the element is not in the
	// set.
	visit := func(c cid.Cid, depth int) bool {
		oldDepth, ok := set[c]

		if (ok && depthLim < 0) || (depthLim >= 0 && depth > depthLim) {
			return false
		}

		if !ok || oldDepth > depth {
			set[c] = depth
			return true
		}
		return false
	}

	// If we have a ProgressTracker, we wrap the visit function to handle it
	v, _ := ctx.Value(progressContextKey).(*ProgressTracker)
	if v == nil {
		return WalkDepth(ctx, GetLinksDirect(ng), root, visit, Concurrent())
	}

	visitProgress := func(c cid.Cid, depth int) bool {
		if visit(c, depth) {
			v.Increment()
			return true
		}
		return false
	}
	return WalkDepth(ctx, GetLinksDirect(ng), root, visitProgress, Concurrent())
}

// GetMany gets many nodes from the DAG at once.
//
// This method may not return all requested nodes (and may or may not return an
// error indicating that it failed to do so. It is up to the caller to verify
// that it received all nodes.
func (n *dagService) GetMany(ctx context.Context, keys []cid.Cid) <-chan *format.NodeOption {
	return getNodesFromBG(ctx, n.Blocks, keys)
}

func dedupKeys(keys []cid.Cid) []cid.Cid {
	set := cid.NewSet()
	for _, c := range keys {
		set.Add(c)
	}
	if set.Len() == len(keys) {
		return keys
	}
	return set.Keys()
}

func getNodesFromBG(ctx context.Context, bs bserv.BlockGetter, keys []cid.Cid) <-chan *format.NodeOption {
	keys = dedupKeys(keys)

	out := make(chan *format.NodeOption, len(keys))
	blocks := bs.GetBlocks(ctx, keys)
	var count int

	go func() {
		defer close(out)
		for {
			select {
			case b, ok := <-blocks:
				if !ok {
					if count != len(keys) {
						out <- &format.NodeOption{Err: fmt.Errorf("failed to fetch all nodes")}
					}
					return
				}

				nd, err := legacy.DecodeNode(ctx, b)
				if err != nil {
					out <- &format.NodeOption{Err: err}
					return
				}

				out <- &format.NodeOption{Node: nd}
				count++

			case <-ctx.Done():
				out <- &format.NodeOption{Err: ctx.Err()}
				return
			}
		}
	}()
	return out
}

// GetLinks is the type of function passed to the EnumerateChildren function(s)
// for getting the children of an IPLD node.
type GetLinks func(context.Context, cid.Cid) ([]*format.Link, error)

// GetLinksWithDAG returns a GetLinks function that tries to use the given
// NodeGetter as a LinkGetter to get the children of a given IPLD node. This may
// allow us to traverse the DAG without actually loading and parsing the node in
// question (if we already have the links cached).
func GetLinksWithDAG(ng format.NodeGetter) GetLinks {
	return func(ctx context.Context, c cid.Cid) ([]*format.Link, error) {
		return format.GetLinks(ctx, ng, c)
	}
}

// defaultConcurrentFetch is the default maximum number of concurrent fetches
// that 'fetchNodes' will start at a time
const defaultConcurrentFetch = 32

// walkOptions represent the parameters of a graph walking algorithm
type walkOptions struct {
	SkipRoot     bool
	Concurrency  int
	ErrorHandler func(c cid.Cid, err error) error
}

// WalkOption is a setter for walkOptions
type WalkOption func(*walkOptions)

func (wo *walkOptions) addHandler(handler func(c cid.Cid, err error) error) {
	if wo.ErrorHandler != nil {
		wo.ErrorHandler = func(c cid.Cid, err error) error {
			return handler(c, wo.ErrorHandler(c, err))
		}
	} else {
		wo.ErrorHandler = handler
	}
}

// SkipRoot is a WalkOption indicating that the root node should skipped
func SkipRoot() WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.SkipRoot = true
	}
}

// Concurrent is a WalkOption indicating that node fetching should be done in
// parallel, with the default concurrency factor.
// NOTE: When using that option, the walk order is *not* guarantee.
// NOTE: It *does not* make multiple concurrent calls to the passed `visit` function.
func Concurrent() WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.Concurrency = defaultConcurrentFetch
	}
}

// Concurrency is a WalkOption indicating that node fetching should be done in
// parallel, with a specific concurrency factor.
// NOTE: When using that option, the walk order is *not* guarantee.
// NOTE: It *does not* make multiple concurrent calls to the passed `visit` function.
func Concurrency(worker int) WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.Concurrency = worker
	}
}

// IgnoreErrors is a WalkOption indicating that the walk should attempt to
// continue even when an error occur.
func IgnoreErrors() WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.addHandler(func(c cid.Cid, err error) error {
			return nil
		})
	}
}

// IgnoreMissing is a WalkOption indicating that the walk should continue when
// a node is missing.
func IgnoreMissing() WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.addHandler(func(c cid.Cid, err error) error {
			if err == format.ErrNotFound {
				return nil
			}
			return err
		})
	}
}

// OnMissing is a WalkOption adding a callback that will be triggered on a missing
// node.
func OnMissing(callback func(c cid.Cid)) WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.addHandler(func(c cid.Cid, err error) error {
			if err == format.ErrNotFound {
				callback(c)
			}
			return err
		})
	}
}

// OnError is a WalkOption adding a custom error handler.
// If this handler return a nil error, the walk will continue.
func OnError(handler func(c cid.Cid, err error) error) WalkOption {
	return func(walkOptions *walkOptions) {
		walkOptions.addHandler(handler)
	}
}

// WalkGraph will walk the dag in order (depth first) starting at the given root.
func Walk(ctx context.Context, getLinks GetLinks, c cid.Cid, visit func(cid.Cid) bool, options ...WalkOption) error {
	visitDepth := func(c cid.Cid, depth int) bool {
		return visit(c)
	}

	return WalkDepth(ctx, getLinks, c, visitDepth, options...)
}

// WalkDepth walks the dag starting at the given root and passes the current
// depth to a given visit function. The visit function can be used to limit DAG
// exploration.
func WalkDepth(ctx context.Context, getLinks GetLinks, c cid.Cid, visit func(cid.Cid, int) bool, options ...WalkOption) error {
	opts := &walkOptions{}
	for _, opt := range options {
		opt(opts)
	}

	if opts.Concurrency > 1 {
		return parallelWalkDepth(ctx, getLinks, c, visit, opts)
	} else {
		return sequentialWalkDepth(ctx, getLinks, c, 0, visit, opts)
	}
}

func sequentialWalkDepth(ctx context.Context, getLinks GetLinks, root cid.Cid, depth int, visit func(cid.Cid, int) bool, options *walkOptions) error {
	if !(options.SkipRoot && depth == 0) {
		if !visit(root, depth) {
			return nil
		}
	}

	links, err := getLinks(ctx, root)
	if err != nil && options.ErrorHandler != nil {
		err = options.ErrorHandler(root, err)
	}
	if err != nil {
		return err
	}

	for _, lnk := range links {
		if err := sequentialWalkDepth(ctx, getLinks, lnk.Cid, depth+1, visit, options); err != nil {
			return err
		}
	}
	return nil
}

// ProgressTracker is used to show progress when fetching nodes.
type ProgressTracker struct {
	Total int
	lk    sync.Mutex
}

// DeriveContext returns a new context with value "progress" derived from
// the given one.
func (p *ProgressTracker) DeriveContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, progressContextKey, p)
}

// Increment adds one to the total progress.
func (p *ProgressTracker) Increment() {
	p.lk.Lock()
	defer p.lk.Unlock()
	p.Total++
}

// Value returns the current progress.
func (p *ProgressTracker) Value() int {
	p.lk.Lock()
	defer p.lk.Unlock()
	return p.Total
}

func parallelWalkDepth(ctx context.Context, getLinks GetLinks, root cid.Cid, visit func(cid.Cid, int) bool, options *walkOptions) error {
	type cidDepth struct {
		cid   cid.Cid
		depth int
	}

	type linksDepth struct {
		links []*format.Link
		depth int
	}

	feed := make(chan cidDepth)
	out := make(chan linksDepth)
	done := make(chan struct{})

	var visitlk sync.Mutex
	var wg sync.WaitGroup

	errChan := make(chan error)
	fetchersCtx, cancel := context.WithCancel(ctx)
	defer wg.Wait()
	defer cancel()
	for i := 0; i < options.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cdepth := range feed {
				ci := cdepth.cid
				depth := cdepth.depth

				var shouldVisit bool

				// bypass the root if needed
				if !(options.SkipRoot && depth == 0) {
					visitlk.Lock()
					shouldVisit = visit(ci, depth)
					visitlk.Unlock()
				} else {
					shouldVisit = true
				}

				if shouldVisit {
					links, err := getLinks(ctx, ci)
					if err != nil && options.ErrorHandler != nil {
						err = options.ErrorHandler(root, err)
					}
					if err != nil {
						select {
						case errChan <- err:
						case <-fetchersCtx.Done():
						}
						return
					}

					outLinks := linksDepth{
						links: links,
						depth: depth + 1,
					}

					select {
					case out <- outLinks:
					case <-fetchersCtx.Done():
						return
					}
				}
				select {
				case done <- struct{}{}:
				case <-fetchersCtx.Done():
				}
			}
		}()
	}
	defer close(feed)

	send := feed
	var todoQueue []cidDepth
	var inProgress int

	next := cidDepth{
		cid:   root,
		depth: 0,
	}

	for {
		select {
		case send <- next:
			inProgress++
			if len(todoQueue) > 0 {
				next = todoQueue[0]
				todoQueue = todoQueue[1:]
			} else {
				next = cidDepth{}
				send = nil
			}
		case <-done:
			inProgress--
			if inProgress == 0 && !next.cid.Defined() {
				return nil
			}
		case linksDepth := <-out:
			for _, lnk := range linksDepth.links {
				cd := cidDepth{
					cid:   lnk.Cid,
					depth: linksDepth.depth,
				}

				if !next.cid.Defined() {
					next = cd
					send = feed
				} else {
					todoQueue = append(todoQueue, cd)
				}
			}
		case err := <-errChan:
			return err

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

var _ format.LinkGetter = &dagService{}
var _ format.NodeGetter = &dagService{}
var _ format.NodeGetter = &sesGetter{}
var _ format.DAGService = &dagService{}
