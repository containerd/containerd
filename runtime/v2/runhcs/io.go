// +build windows

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

package runhcs

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/log"
	runc "github.com/containerd/go-runc"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type pipeSet struct {
	stdin  net.Conn
	stdout net.Conn
	stderr net.Conn
}

// newPipeSet connects to the provided pipe addresses
func newPipeSet(ctx context.Context, stdin, stdout, stderr string, terminal bool) (*pipeSet, error) {
	var (
		err error
		set = &pipeSet{}
	)

	defer func() {
		if err != nil {
			set.Close()
		}
	}()

	g, _ := errgroup.WithContext(ctx)

	dialfn := func(name string, conn *net.Conn) error {
		if name == "" {
			return nil
		}
		dialTimeout := 3 * time.Second
		c, err := winio.DialPipe(name, &dialTimeout)
		if err != nil {
			return errors.Wrapf(err, "failed to connect to %s", name)
		}
		*conn = c
		return nil
	}

	g.Go(func() error {
		return dialfn(stdin, &set.stdin)
	})
	g.Go(func() error {
		return dialfn(stdout, &set.stdout)
	})
	g.Go(func() error {
		return dialfn(stderr, &set.stderr)
	})

	err = g.Wait()
	if err != nil {
		return nil, err
	}
	return set, nil
}

// Close terminates all successfully dialed IO connections
func (p *pipeSet) Close() {
	for _, cn := range []net.Conn{p.stdin, p.stdout, p.stderr} {
		if cn != nil {
			cn.Close()
		}
	}
}

type pipeRelay struct {
	ctx context.Context

	ps *pipeSet
	io runc.IO

	wg   sync.WaitGroup
	once sync.Once
}

func newPipeRelay(ctx context.Context, ps *pipeSet, downstream runc.IO) *pipeRelay {
	pr := &pipeRelay{
		ctx: ctx,
		ps:  ps,
		io:  downstream,
	}
	if ps.stdin != nil {
		go func() {
			if _, err := io.Copy(downstream.Stdin(), ps.stdin); err != nil {
				if err != winio.ErrFileClosed {
					log.G(ctx).WithError(err).Error("error copying stdin to pipe")
				}
			}
		}()
	}
	if ps.stdout != nil {
		pr.wg.Add(1)
		go func() {
			if _, err := io.Copy(ps.stdout, downstream.Stdout()); err != nil {
				log.G(ctx).WithError(err).Error("error copying stdout from pipe")
			}
			pr.wg.Done()
		}()
	}
	if ps.stderr != nil {
		pr.wg.Add(1)
		go func() {
			if _, err := io.Copy(ps.stderr, downstream.Stderr()); err != nil {
				log.G(pr.ctx).WithError(err).Error("error copying stderr from pipe")
			}
			pr.wg.Done()
		}()
	}
	return pr
}

func (pr *pipeRelay) wait() {
	pr.wg.Wait()
}

// closeIO closes stdin to unblock an waiters
func (pr *pipeRelay) closeIO() {
	if pr.ps.stdin != nil {
		pr.ps.Close()
		pr.io.Stdin().Close()
	}
}

// close closes all open pipes
func (pr *pipeRelay) close() {
	pr.once.Do(func() {
		pr.io.Close()
		pr.ps.Close()
	})
}
