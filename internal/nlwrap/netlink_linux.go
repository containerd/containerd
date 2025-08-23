/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

// This implementation follows moby project:
// https://github.com/moby/moby/blob/cea56c1d9c2fae5831f38ae88fba593206985b2b/internal/nlwrap/nlwrap_linux.go
package nlwrap

import (
	"context"
	"errors"

	"github.com/containerd/log"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// Arbitrary limit on max attempts at netlink calls if they are repeatedly interrupted.
const maxAttempts = 5

type Handle struct {
	*netlink.Handle
}

func NewHandle(nlFamilies ...int) (Handle, error) {
	nlh, err := netlink.NewHandle(nlFamilies...)
	if err != nil {
		return Handle{}, err
	}
	return Handle{nlh}, nil
}

func NewHandleAt(ns netns.NsHandle, nlFamilies ...int) (Handle, error) {
	nlh, err := netlink.NewHandleAt(ns, nlFamilies...)
	if err != nil {
		return Handle{}, err
	}
	return Handle{nlh}, nil
}

func (h Handle) Close() {
	if h.Handle != nil {
		h.Handle.Close()
	}
}

func retryOnIntr(f func() error) {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := f(); !errors.Is(err, netlink.ErrDumpInterrupted) {
			return
		}
	}
}

// LinkByName calls nlh.Handle.LinkByName, retrying if necessary. The netlink function
// doesn't normally ask the kernel for a dump of links. But, on an old kernel, it
// will do as a fallback and that dump may get inconsistent results.
func (h Handle) LinkByName(name string) (link netlink.Link, err error) {
	retryOnIntr(func() error {
		link, err = h.Handle.LinkByName(name) //nolint:forbidigo
		return err
	})
	return link, discardErrDumpInterrupted(err)
}

// LinkByName calls netlink.LinkByName, retrying if necessary. The netlink
// function doesn't normally ask the kernel for a dump of links. But, on an old
// kernel, it will do as a fallback and that dump may get inconsistent results.
func LinkByName(name string) (netlink.Link, error) {
	var link netlink.Link
	var err error
	retryOnIntr(func() error {
		link, err = netlink.LinkByName(name) //nolint:forbidigo
		return err
	})
	return link, discardErrDumpInterrupted(err)
}

func discardErrDumpInterrupted(err error) error {
	if errors.Is(err, netlink.ErrDumpInterrupted) {
		// The netlink function has returned possibly-inconsistent data along with the
		// error. Discard the error and return the data. This restores the behaviour of
		// the netlink package prior to v1.2.1, in which NLM_F_DUMP_INTR was ignored in
		// the netlink response.
		log.G(context.TODO()).Warnf("discarding ErrDumpInterrupted: %+v", err)
		return nil
	}
	return err
}
