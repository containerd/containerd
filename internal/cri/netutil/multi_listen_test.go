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

/*
Copyright 2024 The Kubernetes Authors.

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

package net

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCon struct {
	remoteAddr net.Addr
}

func (f *fakeCon) Read(_ []byte) (n int, err error) {
	return 0, nil
}

func (f *fakeCon) Write(_ []byte) (n int, err error) {
	return 0, nil
}

func (f *fakeCon) Close() error {
	return nil
}

func (f *fakeCon) LocalAddr() net.Addr {
	return nil
}

func (f *fakeCon) RemoteAddr() net.Addr {
	return f.remoteAddr
}

func (f *fakeCon) SetDeadline(_ time.Time) error {
	return nil
}

func (f *fakeCon) SetReadDeadline(_ time.Time) error {
	return nil
}

func (f *fakeCon) SetWriteDeadline(_ time.Time) error {
	return nil
}

var _ net.Conn = &fakeCon{}

type fakeListener struct {
	addr         net.Addr
	index        int
	err          error
	closed       atomic.Bool
	connErrPairs []connErrPair
}

func (f *fakeListener) Accept() (net.Conn, error) {
	if f.index < len(f.connErrPairs) {
		index := f.index
		connErr := f.connErrPairs[index]
		f.index++
		return connErr.conn, connErr.err
	}
	for {
		if f.closed.Load() {
			return nil, fmt.Errorf("use of closed network connection")
		}
	}
}

func (f *fakeListener) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *fakeListener) Addr() net.Addr {
	return f.addr
}

var _ net.Listener = &fakeListener{}

func listenFuncFactory(listeners []*fakeListener) func(_ context.Context, network string, address string) (net.Listener, error) {
	index := 0
	return func(_ context.Context, network string, address string) (net.Listener, error) {
		if index < len(listeners) {
			host, portStr, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, err
			}
			listener := listeners[index]
			addr := &net.TCPAddr{
				IP:   net.ParseIP(host),
				Port: port,
			}

			listener.addr = addr
			index++

			if listener.err != nil {
				return nil, listener.err
			}
			return listener, nil
		}
		return nil, nil
	}
}

func TestMultiListen(t *testing.T) {
	testCases := []struct {
		name          string
		network       string
		addrs         []string
		fakeListeners []*fakeListener
		errString     string
	}{
		{
			name:      "unsupported network",
			network:   "udp",
			errString: "network \"udp\" not supported",
		},
		{
			name:      "no host",
			network:   "tcp",
			errString: "no address provided to listen on",
		},
		{
			name:          "valid",
			network:       "tcp",
			addrs:         []string{"127.0.0.1:12345"},
			fakeListeners: []*fakeListener{{connErrPairs: []connErrPair{}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			ml, err := multiListen(ctx, tc.network, tc.addrs, listenFuncFactory(tc.fakeListeners))

			if tc.errString != "" {
				assertError(t, tc.errString, err)
			} else {
				assertNoError(t, err)
			}
			if ml != nil {
				err = ml.Close()
				if err != nil {
					t.Errorf("Did not expect error: %v", err)
				}
			}
		})
	}
}

func TestMultiListen_Addr(t *testing.T) {
	ctx := context.TODO()
	ml, err := multiListen(ctx, "tcp", []string{"10.10.10.10:5000", "192.168.1.10:5000", "127.0.0.1:5000"}, listenFuncFactory(
		[]*fakeListener{{}, {}, {}},
	))
	if err != nil {
		t.Errorf("Did not expect error: %v", err)
	}

	if ml.Addr().String() != "10.10.10.10:5000" {
		t.Errorf("Expected '10.10.10.10:5000' but got '%s'", ml.Addr().String())
	}

	err = ml.Close()
	if err != nil {
		t.Errorf("Did not expect error: %v", err)
	}
}

func TestMultiListen_Addrs(t *testing.T) {
	ctx := context.TODO()
	addrs := []string{"10.10.10.10:5000", "192.168.1.10:5000", "127.0.0.1:5000"}
	ml, err := multiListen(ctx, "tcp", addrs, listenFuncFactory(
		[]*fakeListener{{}, {}, {}},
	))
	if err != nil {
		t.Errorf("Did not expect error: %v", err)
	}

	gotAddrs := ml.(*multiListener).Addrs()
	for i := range gotAddrs {
		if gotAddrs[i].String() != addrs[i] {
			t.Errorf("expected %q; got %q", addrs[i], gotAddrs[i].String())
		}

	}

	err = ml.Close()
	if err != nil {
		t.Errorf("Did not expect error: %v", err)
	}
}

func TestMultiListen_Close(t *testing.T) {
	testCases := []struct {
		name          string
		addrs         []string
		runner        func(listener net.Listener, acceptCalls int) error
		fakeListeners []*fakeListener
		acceptCalls   int
		errString     string
	}{
		{
			name:  "close",
			addrs: []string{"10.10.10.10:5000", "192.168.1.10:5000", "127.0.0.1:5000"},
			runner: func(ml net.Listener, acceptCalls int) error {
				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}
				}
				err := ml.Close()
				if err != nil {
					return err
				}
				return nil
			},
			fakeListeners: []*fakeListener{{}, {}, {}},
		},
		{
			name:  "close with pending connections",
			addrs: []string{"10.10.10.10:5001", "192.168.1.10:5002", "127.0.0.1:5003"},
			runner: func(ml net.Listener, acceptCalls int) error {
				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}
				}
				err := ml.Close()
				if err != nil {
					return err
				}
				return nil
			},
			fakeListeners: []*fakeListener{{
				connErrPairs: []connErrPair{{
					conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("10.10.10.10"), Port: 50001}},
				}}}, {
				connErrPairs: []connErrPair{{
					conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 50002}},
				},
				}}, {
				connErrPairs: []connErrPair{{
					conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50003}},
				}},
			}},
		},
		{
			name:  "close with no pending connections",
			addrs: []string{"10.10.10.10:3001", "192.168.1.10:3002", "127.0.0.1:3003"},
			runner: func(ml net.Listener, acceptCalls int) error {
				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}
				}
				err := ml.Close()
				if err != nil {
					return err
				}
				return nil
			},
			fakeListeners: []*fakeListener{{
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("10.10.10.10"), Port: 50001}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 50002}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50003}}},
				},
			}},
			acceptCalls: 3,
		},
		{
			name:  "close on close",
			addrs: []string{"10.10.10.10:5000", "192.168.1.10:5000", "127.0.0.1:5000"},
			runner: func(ml net.Listener, acceptCalls int) error {
				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}
				}
				err := ml.Close()
				if err != nil {
					return err
				}

				err = ml.Close()
				if err != nil {
					return err
				}
				return nil
			},
			fakeListeners: []*fakeListener{{}, {}, {}},
			errString:     "use of closed network connection",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			ml, err := multiListen(ctx, "tcp", tc.addrs, listenFuncFactory(tc.fakeListeners))
			if err != nil {
				t.Errorf("Did not expect error: %v", err)
			}
			err = tc.runner(ml, tc.acceptCalls)
			if tc.errString != "" {
				assertError(t, tc.errString, err)
			} else {
				assertNoError(t, err)
			}

			for _, f := range tc.fakeListeners {
				if !f.closed.Load() {
					t.Errorf("Expeted sub-listener to be closed")
				}
			}
		})
	}
}

func TestMultiListen_Accept(t *testing.T) {
	testCases := []struct {
		name          string
		addrs         []string
		runner        func(listener net.Listener, acceptCalls int) error
		fakeListeners []*fakeListener
		acceptCalls   int
		errString     string
	}{
		{
			name:  "accept all connections",
			addrs: []string{"10.10.10.10:3000", "192.168.1.103:4000", "127.0.0.1:5000"},
			runner: func(ml net.Listener, acceptCalls int) error {
				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}
				}
				err := ml.Close()
				if err != nil {
					return err
				}
				return nil
			},
			fakeListeners: []*fakeListener{{
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("10.10.10.10"), Port: 50001}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 50002}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50003}}},
				},
			}},
			acceptCalls: 3,
		},
		{
			name:  "accept some connections",
			addrs: []string{"10.10.10.10:3000", "192.168.1.103:4000", "172.16.20.10:5000", "127.0.0.1:6000"},
			runner: func(ml net.Listener, acceptCalls int) error {

				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}

				}
				err := ml.Close()
				if err != nil {
					return err
				}
				return nil
			},
			fakeListeners: []*fakeListener{{
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("10.10.10.10"), Port: 30001}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 40001}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 40002}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("172.16.20.10"), Port: 50001}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("172.16.20.10"), Port: 50002}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("172.16.20.10"), Port: 50003}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("172.16.20.10"), Port: 50004}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 60001}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 60002}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 60003}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 60004}}},
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 60005}}},
				},
			}},
			acceptCalls: 3,
		},
		{
			name:  "accept on closed listener",
			addrs: []string{"10.10.10.10:3001", "192.168.1.10:3002", "127.0.0.1:3003"},
			runner: func(ml net.Listener, acceptCalls int) error {
				err := ml.Close()
				if err != nil {
					return err
				}
				for i := 0; i < acceptCalls; i++ {
					_, err := ml.Accept()
					if err != nil {
						return err
					}
				}
				return nil
			},
			fakeListeners: []*fakeListener{{
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("10.10.10.10"), Port: 50001}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 50002}}},
				}}, {
				connErrPairs: []connErrPair{
					{conn: &fakeCon{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50003}}},
				},
			}},
			acceptCalls: 1,
			errString:   "use of closed network connection",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			ml, err := multiListen(ctx, "tcp", tc.addrs, listenFuncFactory(tc.fakeListeners))
			if err != nil {
				t.Errorf("Did not expect error: %v", err)
			}

			err = tc.runner(ml, tc.acceptCalls)
			if tc.errString != "" {
				assertError(t, tc.errString, err)
			} else {
				assertNoError(t, err)
			}
		})
	}
}

func TestMultiListen_HTTP(t *testing.T) {
	ctx := context.TODO()
	ml, err := MultiListen(ctx, "tcp", ":0", ":0", ":0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addrs := ml.(*multiListener).Addrs()
	if len(addrs) != 3 {
		t.Fatalf("expected 3 listeners, got %v", addrs)
	}

	// serve http on multi-listener
	handler := func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "hello")
	}
	server := http.Server{
		Handler:           http.HandlerFunc(handler),
		ReadHeaderTimeout: 0,
	}
	go func() { _ = server.Serve(ml) }()
	defer server.Close()

	// Wait for server
	awake := false
	for i := 0; i < 5; i++ {
		_, err = http.Get("http://" + addrs[0].String())
		if err == nil {
			awake = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !awake {
		t.Fatalf("http server did not respond in time")
	}

	// HTTP GET on each address.
	for _, addr := range addrs {
		_, err = http.Get("http://" + addr.String())
		if err != nil {
			t.Errorf("error connecting to %q: %v", addr.String(), err)
		}
	}
}

func assertError(t *testing.T, errString string, err error) {
	if err == nil {
		t.Errorf("Expected error '%s' but got none", errString)
	}
	if err.Error() != errString {
		t.Errorf("Expected error '%s' but got '%s'", errString, err.Error())
	}
}

func assertNoError(t *testing.T, err error) {
	if err != nil {
		t.Errorf("Did not expect error: %v", err)
	}
}
