package goprocess

// Background returns the "bgProcess" Process: a statically allocated
// process that can _never_ close. It also never enters Closing() state.
// Calling Background().Close() will hang indefinitely.
func Background() Process {
	return background
}

var background = new(bgProcess)

type bgProcess struct{}

func (*bgProcess) WaitFor(q Process)         {}
func (*bgProcess) AddChildNoWait(q Process)  {}
func (*bgProcess) AddChild(q Process)        {}
func (*bgProcess) Close() error              { select {} }
func (*bgProcess) CloseAfterChildren() error { select {} }
func (*bgProcess) Closing() <-chan struct{}  { return nil }
func (*bgProcess) Closed() <-chan struct{}   { return nil }
func (*bgProcess) Err() error                { select {} }

func (*bgProcess) SetTeardown(tf TeardownFunc) {
	panic("can't set teardown on bgProcess process")
}
func (*bgProcess) Go(f ProcessFunc) Process {
	child := newProcess(nil)
	go func() {
		f(child)
		child.Close()
	}()
	return child
}
