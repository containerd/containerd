package format

import (
	"context"
	"errors"
)

// Walker provides methods to move through a DAG of nodes that implement
// the `NavigableNode` interface. It uses iterative algorithms (instead
// of recursive ones) that expose the `path` of nodes from the root to
// the `ActiveNode` it currently points to.
//
// It provides multiple ways to walk through the DAG (e.g. `Iterate`
// and `Seek`). When using them, you provide a Visitor function that
// will be called for each node the Walker traverses. The Visitor can
// read data from those nodes and, optionally, direct the movement of
// the Walker by calling `Pause` (to stop traversing and return) or
// `NextChild` (to skip a child and its descendants). See the DAG reader
// in `github.com/ipfs/go-unixfs/io/dagreader.go` for a usage example.
// TODO: This example isn't merged yet.
type Walker struct {

	// Sequence of nodes in the DAG from the root to the `ActiveNode`, each
	// position in the slice being the parent of the next one. The `ActiveNode`
	// resides in the position indexed by `currentDepth` (the slice may contain
	// more elements past that point but they should be ignored since the slice
	// is not truncated to leverage the already allocated space).
	//
	// Every time `down` is called the `currentDepth` increases and the child
	// of the `ActiveNode` is inserted after it (effectively becoming the new
	// `ActiveNode`).
	//
	// The slice must *always* have a length bigger than zero with the root
	// of the DAG at the first position (empty DAGs are not valid).
	path []NavigableNode

	// Depth of the `ActiveNode`. It grows downwards, root being 0, its child 1,
	// and so on. It controls the effective length of `path` and `childIndex`.
	//
	// A currentDepth of -1 signals the start case of a new `Walker` that hasn't
	// moved yet. Although this state is an invalid index to the slices, it
	// allows to centralize all the visit calls in the `down` move (starting at
	// zero would require a special visit case inside every walk operation like
	// `Iterate()` and `Seek`). This value should never be returned to after
	// the first `down` movement, moving up from the root should always return
	// `errUpOnRoot`.
	currentDepth int

	// This slice has the index of the child each node in `path` is pointing
	// to. The child index in the node can be set past all of its child nodes
	// (having a value equal to `ChildTotal`) to signal it has visited (or
	// skipped) all of them. A leaf node with no children that has its index
	// in zero would also comply with this format.
	//
	// Complement to `path`, not only do we need to know which nodes have been
	// traversed to reach the `ActiveNode` but also which child nodes they are
	// to correctly have the active path of the DAG. (Reword this paragraph.)
	childIndex []uint

	// Flag to signal that a pause in the current walk operation has been
	// requested by the user inside `Visitor`.
	pauseRequested bool

	// Used to pass information from the central `Walker` structure to the
	// distributed `NavigableNode`s (to have a centralized configuration
	// structure to control the behavior of all of them), e.g., to tell
	// the `NavigableIPLDNode` which context should be used to load node
	// promises (but this could later be used in more elaborate ways).
	ctx context.Context
}

// `Walker` implementation details:
//
// The `Iterate` and `Seek` walk operations are implemented through two
// basic move methods `up` and `down`, that change which node is the
// `ActiveNode` (modifying the `path` that leads to it). The `NextChild`
// method allows to change which child the `ActiveNode` is pointing to
// in order to change the direction of the descent.
//
// The `down` method is the analogous of a recursive call and the one in
// charge of visiting (possible new) nodes (through `Visitor`) and performing
// some user-defined logic. A `Pause` method is available to interrupt the
// current walk operation after visiting a node.
//
// Key terms and concepts:
// * Walk operation (e.g., `Iterate`).
// * Move methods: `up` and `down`.
// * Active node.
// * Path to the active node.

// Function called each time a node is arrived upon in a walk operation
// through the `down` method (not when going back `up`). It is the main
// API to implement DAG functionality (e.g., read and seek a file DAG)
// on top of the `Walker` structure.
//
// Its argument is the current `node` being visited (the `ActiveNode`).
// Any error it returns (apart from the internal `errPauseWalkOperation`)
// will be forwarded to the caller of the walk operation (pausing it).
//
// Any of the exported methods of this API should be allowed to be called
// from within this method, e.g., `NextChild`.
// TODO: Check that. Can `ResetPosition` be called without breaking
// the `Walker` integrity?
type Visitor func(node NavigableNode) error

// NavigableNode is the interface the nodes of a DAG need to implement in
// order to be traversed by the `Walker`.
type NavigableNode interface {

	// FetchChild returns the child of this node pointed to by `childIndex`.
	// A `Context` stored in the `Walker` is passed (`ctx`) that may contain
	// configuration attributes stored by the user before initiating the
	// walk operation.
	FetchChild(ctx context.Context, childIndex uint) (NavigableNode, error)

	// ChildTotal returns the number of children of the `ActiveNode`.
	ChildTotal() uint

	// TODO: Evaluate providing the `Cleanup` and `Reset` methods.

	// Cleanup is an optional method that is called by the `Walker` when
	// this node leaves the active `path`, i.e., when this node is the
	// `ActiveNode` and the `up` movement is called.
	//Cleanup()
	// Allow this method to return an error? That would imply
	// modifying the `Walker` API, `up()` would now return an error
	// different than `errUpOnRoot`.

	// Reset is an optional function that is called by the `Walker` when
	// `ResetPosition` is called, it is only applied to the root node
	// of the DAG.
	//Reset()
}

// NewWalker creates a new `Walker` structure from a `root`
// NavigableNode.
func NewWalker(ctx context.Context, root NavigableNode) *Walker {
	return &Walker{
		ctx: ctx,

		path:       []NavigableNode{root},
		childIndex: []uint{0},

		currentDepth: -1,
		// Starting position, "on top" of the root node, see `currentDepth`.
	}
}

// ActiveNode returns the `NavigableNode` that `Walker` is pointing
// to at the moment. It changes when `up` or `down` is called.
func (w *Walker) ActiveNode() NavigableNode {
	return w.path[w.currentDepth]
	// TODO: Add a check for the initial state of `currentDepth` -1?
}

// ErrDownNoChild signals there is no child at `ActiveChildIndex` in the
// `ActiveNode` to go down to.
var ErrDownNoChild = errors.New("can't go down, the child does not exist")

// errUpOnRoot signals the end of the DAG after returning to the root.
var errUpOnRoot = errors.New("can't go up, already on root")

// EndOfDag wraps the `errUpOnRoot` and signals to the user that the
// entire DAG has been iterated.
var EndOfDag = errors.New("end of DAG")

// ErrNextNoChild signals the end of this parent child nodes.
var ErrNextNoChild = errors.New("can't go to the next child, no more child nodes in this parent")

// errPauseWalkOperation signals the pause of the walk operation.
var errPauseWalkOperation = errors.New("pause in the current walk operation")

// ErrNilVisitor signals the lack of a `Visitor` function.
var ErrNilVisitor = errors.New("no Visitor function specified")

// Iterate the DAG through the DFS pre-order walk algorithm, going down
// as much as possible, then `NextChild` to the other siblings, and then up
// (to go down again). The position is saved throughout iterations (and
// can be previously set in `Seek`) allowing `Iterate` to be called
// repeatedly (after a `Pause`) to continue the iteration.
//
// This function returns the errors received from `down` (generated either
// inside the `Visitor` call or any other errors while fetching the child
// nodes), the rest of the move errors are handled within the function and
// are not returned.
func (w *Walker) Iterate(visitor Visitor) error {

	// Iterate until either: the end of the DAG (`errUpOnRoot`), a `Pause`
	// is requested (`errPauseWalkOperation`) or an error happens (while
	// going down).
	for {

		// First, go down as much as possible.
		for {
			err := w.down(visitor)

			if err == ErrDownNoChild {
				break
				// Can't keep going down from this node, try to move Next.
			}

			if err == errPauseWalkOperation {
				return nil
				// Pause requested, `errPauseWalkOperation` is just an internal
				// error to signal to pause, don't pass it along.
			}

			if err != nil {
				return err
				// `down` is the only movement that can return *any* error.
			}
		}

		// Can't move down anymore, turn to the next child in the `ActiveNode`
		// to go down a different path. If there are no more child nodes
		// available, go back up.
		for {
			err := w.NextChild()
			if err == nil {
				break
				// No error, it turned to the next child. Try to go down again.
			}

			// It can't go Next (`ErrNextNoChild`), try to move up.
			err = w.up()
			if err != nil {
				// Can't move up, on the root again (`errUpOnRoot`).
				return EndOfDag
			}

			// Moved up, try `NextChild` again.
		}

		// Turned to the next child (after potentially many up moves),
		// try going down again.
	}
}

// Seek a specific node in a downwards manner. The `Visitor` should be
// used to steer the seek selecting at each node which child will the
// seek continue to (extending the `path` in that direction) or pause it
// (if the desired node has been found). The seek always starts from
// the root. It modifies the position so it shouldn't be used in-between
// `Iterate` calls (it can be used to set the position *before* iterating).
// If the visitor returns any non-`nil` errors the seek will stop.
//
// TODO: The seek could be extended to seek from the current position.
// (Is there something in the logic that would prevent it at the moment?)
func (w *Walker) Seek(visitor Visitor) error {

	if visitor == nil {
		return ErrNilVisitor
		// Although valid, there is no point in calling `Seek` without
		// any extra logic, it would just go down to the leftmost leaf,
		// so this would probably be a user error.
	}

	// Go down until it the desired node is found (that will be signaled
	// pausing the seek with `errPauseWalkOperation`) or a leaf node is
	// reached (end of the DAG).
	for {
		err := w.down(visitor)

		if err == errPauseWalkOperation {
			return nil
			// Found the node, `errPauseWalkOperation` is just an internal
			// error to signal to pause, don't pass it along.
		}

		if err == ErrDownNoChild {
			return nil
			// Can't keep going down from this node, either at a leaf node
			// or the `Visitor` has moved the child index past the
			// available index (probably because none indicated that the
			// target node could be down from there).
		}

		if err != nil {
			return err
			// `down()` is the only movement that can return *any* error.
		}
	}
	// TODO: Copied from the first part of `Iterate()` (although conceptually
	// different from it). Could this be encapsulated in a function to avoid
	// repeating code? The way the pause signal is handled it wouldn't seem
	// very useful: the `errPauseWalkOperation` needs to be processed at this
	// depth to return from the function (and pause the seek, returning
	// from another function here wouldn't cause it to stop).
}

// Go down one level in the DAG to the child of the `ActiveNode`
// pointed to by `ActiveChildIndex` and perform some logic on it by
// through the user-specified `visitor`.
//
// This should always be the first move in any walk operation
// (to visit the root node and move the `currentDepth` away
// from the negative value).
func (w *Walker) down(visitor Visitor) error {
	child, err := w.fetchChild()
	if err != nil {
		return err
	}

	w.extendPath(child)

	return w.visitActiveNode(visitor)
}

// Fetch the child from the `ActiveNode` through the `FetchChild`
// method of the `NavigableNode` interface.
func (w *Walker) fetchChild() (NavigableNode, error) {
	if w.currentDepth == -1 {
		// First time `down()` is called, `currentDepth` is -1,
		// return the root node. Don't check available child nodes
		// (as the `Walker` is not actually on any node just yet
		// and `ActiveChildIndex` is of no use yet).
		return w.path[0], nil
	}

	// Check if the child to fetch exists.
	if w.ActiveChildIndex() >= w.ActiveNode().ChildTotal() {
		return nil, ErrDownNoChild
	}

	return w.ActiveNode().FetchChild(w.ctx, w.ActiveChildIndex())

	// TODO: Maybe call `extendPath` here and hide it away
	// from `down`.
}

// Increase the `currentDepth` and extend the `path` to the fetched
// `child` node (which now becomes the new `ActiveNode`)
func (w *Walker) extendPath(child NavigableNode) {
	w.currentDepth++

	// Extend the slices if needed (doubling its capacity).
	if w.currentDepth >= len(w.path) {
		w.path = append(w.path, make([]NavigableNode, len(w.path))...)
		w.childIndex = append(w.childIndex, make([]uint, len(w.childIndex))...)
		// TODO: Check the performance of this grow mechanism.
	}

	// `child` now becomes the `ActiveNode()`.
	w.path[w.currentDepth] = child
	w.childIndex[w.currentDepth] = 0
}

// Call the `Visitor` on the `ActiveNode`. This function should only be
// called from `down`. This is a wrapper function to `Visitor` to process
// the `Pause` signal and do other minor checks (taking this logic away
// from `down`).
func (w *Walker) visitActiveNode(visitor Visitor) error {
	if visitor == nil {
		return nil
		// No need to check `pauseRequested` as `Pause` should
		// only be called from within the `Visitor`.
	}

	err := visitor(w.ActiveNode())

	if w.pauseRequested {
		// If a pause was requested make sure an error is returned
		// that will cause the current walk operation to return. If
		// `Visitor` didn't return an error set an artificial one
		// generated by the `Walker`.
		if err == nil {
			err = errPauseWalkOperation
		}

		w.pauseRequested = false
	}

	return err
}

// Go up from the `ActiveNode`. The only possible error this method
// can return is to signal it's already at the root and can't go up.
func (w *Walker) up() error {
	if w.currentDepth < 1 {
		return errUpOnRoot
	}

	w.currentDepth--

	// w.ActiveNode().Cleanup()
	// If `Cleanup` is supported this would be the place to call it.

	return nil
}

// NextChild increases the child index of the `ActiveNode` to point
// to the next child (which may exist or may be the end of the available
// child nodes).
//
// This method doesn't change the `ActiveNode`, it just changes where
// is it pointing to next, it could be interpreted as "turn to the next
// child".
func (w *Walker) NextChild() error {
	w.incrementActiveChildIndex()

	if w.ActiveChildIndex() == w.ActiveNode().ChildTotal() {
		return ErrNextNoChild
		// At the end of the available children, signal it.
	}

	return nil
}

// incrementActiveChildIndex increments the child index of the `ActiveNode` to
// point to the next child (if it exists) or to the position past all of
// the child nodes (`ChildTotal`) to signal that all of its children have
// been visited/skipped (if already at that last position, do nothing).
func (w *Walker) incrementActiveChildIndex() {
	if w.ActiveChildIndex()+1 <= w.ActiveNode().ChildTotal() {
		w.childIndex[w.currentDepth]++
	}
}

// ActiveChildIndex returns the index of the child the `ActiveNode()`
// is pointing to.
func (w *Walker) ActiveChildIndex() uint {
	return w.childIndex[w.currentDepth]
}

// SetContext changes the internal `Walker` (that is provided to the
// `NavigableNode`s when calling `FetchChild`) with the one passed
// as argument.
func (w *Walker) SetContext(ctx context.Context) {
	w.ctx = ctx
}

// Pause the current walk operation. This function must be called from
// within the `Visitor` function.
func (w *Walker) Pause() {
	w.pauseRequested = true
}
