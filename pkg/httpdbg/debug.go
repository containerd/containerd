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

package httpdbg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"

	"github.com/containerd/log"
)

// debugTransport wraps the underlying http.RoundTripper interface and dumps all requests/responses to the writer.
type debugTransport struct {
	transport http.RoundTripper
	writer    io.Writer
}

// RoundTrip dumps request/responses and executes the request using the underlying transport.
func (t debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	in, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		return nil, fmt.Errorf("failed to dump request: %w", err)
	}

	if _, err := t.writer.Write(in); err != nil {
		return nil, err
	}

	resp, err := t.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	out, err := httputil.DumpResponse(resp, true)
	if err != nil {
		return nil, fmt.Errorf("failed to dump response: %w", err)
	}

	if _, err := t.writer.Write(out); err != nil {
		return nil, err
	}

	return resp, err
}

// DumpRequests wraps the underlying http.Client transport to logs all requests/responses to the log.
func DumpRequests(ctx context.Context, client *http.Client, writer io.Writer) {
	if writer == nil {
		writer = log.G(ctx).Writer()
	}

	client.Transport = debugTransport{
		transport: client.Transport,
		writer:    writer,
	}
}

// NewDebugClientTrace returns a Go http trace client predefined to write DNS and connection
// information to the log. This is used via the --http-trace flag on push and pull operations in ctr.
func NewDebugClientTrace(ctx context.Context) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(dnsInfo httptrace.DNSStartInfo) {
			log.G(ctx).WithField("host", dnsInfo.Host).Debugf("DNS lookup")
		},
		DNSDone: func(dnsInfo httptrace.DNSDoneInfo) {
			if dnsInfo.Err != nil {
				log.G(ctx).WithField("lookup_err", dnsInfo.Err).Debugf("DNS lookup error")
			} else {
				log.G(ctx).WithField("result", dnsInfo.Addrs[0].String()).WithField("coalesced", dnsInfo.Coalesced).Debugf("DNS lookup complete")
			}
		},
		GotConn: func(connInfo httptrace.GotConnInfo) {
			remoteAddr := "<nil>"
			if addr := connInfo.Conn.RemoteAddr(); addr != nil {
				remoteAddr = addr.String()
			}

			log.G(ctx).WithField("reused", connInfo.Reused).WithField("remote_addr", remoteAddr).Debugf("Connection successful")
		},
	}
}

type traceTransport struct {
	tracer    *httptrace.ClientTrace
	transport http.RoundTripper
}

func (t traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), t.tracer))
	return t.transport.RoundTrip(req)
}

// DumpTraces attaches HTTP events tracer to the client's transport.
func DumpTraces(ctx context.Context, client *http.Client) {
	client.Transport = traceTransport{
		tracer:    NewDebugClientTrace(ctx),
		transport: client.Transport,
	}
}

// WithClientTrace wraps context with a httptrace.ClientTrace for debugging.
func WithClientTrace(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, NewDebugClientTrace(ctx))
}
