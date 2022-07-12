package runtime

import (
	"context"
	"sync"

	"github.com/containerd/ttrpc"
	"github.com/pkg/errors"
)

var (
	ErrTTrpcClientNotExists         = errors.New("ttrpc client does not exist")
	ErrTTrpcClientOnCloseFuncExists = errors.New("the onCloseFunc of container is already exist")
	ErrTTrpcClientOnUnixSockExists  = errors.New("ttrpc client of unix sock already exist")
	ErrTTrpcClientUnknown           = errors.New("ttrpc client list unknown errors")
)

type TTrpcClient struct {
	tclient      *ttrpc.Client
	onCloseFuncs map[string]func()
}

func NewTTrpcClient(client *ttrpc.Client) *TTrpcClient {
	return &TTrpcClient{
		tclient:      client,
		onCloseFuncs: make(map[string]func()),
	}
}

type TTrpcClientList struct {
	mu            sync.Mutex
	ttrpc_clients map[string]*TTrpcClient
}

func NewTTrpcClientList() *TTrpcClientList {
	return &TTrpcClientList{
		ttrpc_clients: make(map[string]*TTrpcClient),
	}
}

func (tl *TTrpcClientList) Get(ctx context.Context, conn_addr string) (*ttrpc.Client, error) {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	tc, ok := tl.ttrpc_clients[conn_addr]
	if !ok {
		return nil, ErrTTrpcClientNotExists
	}
	if tc.tclient == nil {
		return nil, ErrTTrpcClientNotExists
	}
	return tc.tclient, nil
}

func (tl *TTrpcClientList) DoClientClose(ctx context.Context, conn_addr string, clientCloseFunc func() error) (bool, error) {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	t, ok := tl.ttrpc_clients[conn_addr]
	if !ok {
		return true, clientCloseFunc()
	}

	if len(t.onCloseFuncs) == 0 {
		delete(tl.ttrpc_clients, conn_addr)
		return true, clientCloseFunc()
	} else {
		return false, nil
	}
}

func (tl *TTrpcClientList) DoCloseFunc(ctx context.Context, conn_addr string, id string) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	t, ok := tl.ttrpc_clients[conn_addr]
	if !ok {
		return ErrTTrpcClientNotExists
	}

	closeF, ok := t.onCloseFuncs[id]
	if ok {
		closeF()
		delete(t.onCloseFuncs, id)
	}

	if len(t.onCloseFuncs) == 0 {
		delete(tl.ttrpc_clients, conn_addr)
	}

	return nil
}

func (tl *TTrpcClientList) DoAllCloseFuncs(conn_addr string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	tc, ok := tl.ttrpc_clients[conn_addr]
	if ok {
		for k, v := range tc.onCloseFuncs {
			v()
			delete(tc.onCloseFuncs, k)
		}
		delete(tl.ttrpc_clients, conn_addr)
	}
}

func (tl *TTrpcClientList) Inc(ctx context.Context, conn_addr string, client *ttrpc.Client, id string, onCloseFunc func()) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	tc, ok := tl.ttrpc_clients[conn_addr]
	if ok {
		if tc.tclient == client {
			_, ok := tc.onCloseFuncs[id]
			if ok {
				return ErrTTrpcClientOnCloseFuncExists
			} else {
				tc.onCloseFuncs[id] = onCloseFunc
				return nil
			}
		} else {
			return ErrTTrpcClientOnUnixSockExists
		}
	}

	tc = NewTTrpcClient(client)
	tc.onCloseFuncs[id] = onCloseFunc
	tl.ttrpc_clients[conn_addr] = tc
	return nil
}
