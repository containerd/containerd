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

package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes"
)

type dockerPusher struct {
	*dockerBase
	object string

	// TODO: namespace tracker
	tracker StatusTracker
}

// Writer implements Ingester API of content store. This allows the client
// to receive ErrUnavailable when there is already an on-going upload.
// Note that the tracker MUST implement StatusTrackLocker interface to avoid
// race condition on StatusTracker.
func (p dockerPusher) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	var wOpts content.WriterOpts
	for _, opt := range opts {
		if err := opt(&wOpts); err != nil {
			return nil, err
		}
	}
	if wOpts.Ref == "" {
		return nil, fmt.Errorf("ref must not be empty: %w", errdefs.ErrInvalidArgument)
	}
	if wOpts.Desc.Digest == "" {
		return nil, fmt.Errorf("descriptor digest must not be empty: %w", errdefs.ErrInvalidArgument)
	}
	if wOpts.Desc.MediaType == "" {
		return nil, fmt.Errorf("descriptor media type must not be empty: %w", errdefs.ErrInvalidArgument)
	}
	return p.push(ctx, wOpts.Desc, wOpts.Ref, true)
}

func (p dockerPusher) Push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	return p.push(ctx, desc, remotes.MakeRefKey(ctx, desc), false)
}

func (p dockerPusher) push(ctx context.Context, desc ocispec.Descriptor, ref string, unavailableOnFail bool) (content.Writer, error) {
	if l, ok := p.tracker.(StatusTrackLocker); ok {
		l.Lock(ref)
		defer l.Unlock(ref)
	}
	ctx, err := ContextWithRepositoryScope(ctx, p.refspec, true)
	if err != nil {
		return nil, err
	}
	if p.dockerBase.warningHandler != nil {
		ctx = context.WithValue(ctx, warningSourceKey{}, WarningSource{
			Desc:   &desc,
			Digest: &desc.Digest,
		})
	}
	status, err := p.tracker.GetStatus(ref)
	if err == nil {
		if status.Committed && status.Offset == status.Total {
			return nil, fmt.Errorf("ref %v: %w", ref, errdefs.ErrAlreadyExists)
		}
		if unavailableOnFail && status.ErrClosed == nil {
			// Another push of this ref is happening elsewhere. The rest of function
			// will continue only when `errdefs.IsNotFound(err) == true` (i.e. there
			// is no actively-tracked ref already).
			return nil, fmt.Errorf("push is on-going: %w", errdefs.ErrUnavailable)
		}
		// TODO: Handle incomplete status
	} else if !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	hosts := p.filterHosts(HostCapabilityPush)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no push hosts: %w", errdefs.ErrNotFound)
	}

	var (
		isManifest bool
		existCheck []string
		host       = hosts[0]
	)

	if images.IsManifestType(desc.MediaType) || images.IsIndexType(desc.MediaType) {
		isManifest = true
		existCheck = getManifestPath(p.object, desc.Digest)
	} else {
		existCheck = []string{"blobs", desc.Digest.String()}
	}

	req := p.request(host, http.MethodHead, existCheck...)
	if err := req.addNamespace(p.refspec.Hostname()); err != nil {
		return nil, err
	}
	req.header.Set("Accept", strings.Join([]string{desc.MediaType, `*/*`}, ", "))

	log.G(ctx).WithField("url", req.sanitizedURL()).Debugf("checking and pushing to")

	resp, err := req.doWithRetries(ctx, true)
	if err != nil {
		if !errors.Is(err, ErrInvalidAuthorization) {
			return nil, err
		}
		log.G(ctx).WithError(err).Debugf("Unable to check existence, continuing with push")
	} else {
		if resp.StatusCode == http.StatusOK {
			var exists bool
			if isManifest && existCheck[1] != desc.Digest.String() {
				dgstHeader := digest.Digest(resp.Header.Get("Docker-Content-Digest"))
				if dgstHeader == desc.Digest {
					exists = true
				}
			} else {
				exists = true
			}

			if exists {
				p.tracker.SetStatus(ref, Status{
					Committed: true,
					PushStatus: PushStatus{
						Exists: true,
					},
					Status: content.Status{
						Ref: ref,
						// TODO: Set updated time?
					},
				})
				resp.Body.Close()
				return nil, fmt.Errorf("content %v on remote: %w", desc.Digest, errdefs.ErrAlreadyExists)
			}
		} else if resp.StatusCode != http.StatusNotFound {
			err := unexpectedResponseErr(resp)
			// A HEAD 403 carries no body, so issue a follow-up GET to the
			// same URL to surface the registry's error details for diagnostics.
			if resp.StatusCode == http.StatusForbidden && req.method == http.MethodHead {
				err = withGETErrorBody(ctx, err, resp, func() (*http.Response, error) {
					getReq := p.request(host, http.MethodGet, existCheck...)
					getReq.header.Set("Accept", strings.Join([]string{desc.MediaType, `*/*`}, ", "))
					if addErr := getReq.addNamespace(p.refspec.Hostname()); addErr != nil {
						return nil, addErr
					}
					return getReq.doWithRetries(ctx, false)
				})
			}
			log.G(ctx).WithError(err).Debug("unexpected response")
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
	}

	if isManifest {
		putPath := getManifestPath(p.object, desc.Digest)
		req = p.request(host, http.MethodPut, putPath...)
		if err := req.addNamespace(p.refspec.Hostname()); err != nil {
			return nil, err
		}
		req.header.Add("Content-Type", desc.MediaType)
	} else {
		// Start upload request
		req = p.request(host, http.MethodPost, "blobs", "uploads/")
		if err := req.addNamespace(p.refspec.Hostname()); err != nil {
			return nil, err
		}

		mountedFrom := ""
		var resp *http.Response
		if fromRepo := selectRepositoryMountCandidate(p.refspec, desc.Annotations); fromRepo != "" {
			preq := requestWithMountFrom(req, desc.Digest.String(), fromRepo)
			pctx := ContextWithAppendPullRepositoryScope(ctx, fromRepo)

			// NOTE: the fromRepo might be private repo and
			// auth service still can grant token without error.
			// but the post request will fail because of 401.
			//
			// for the private repo, we should remove mount-from
			// query and send the request again.
			resp, err = preq.doWithRetries(pctx, true)
			if err != nil {
				if !errors.Is(err, ErrInvalidAuthorization) {
					return nil, fmt.Errorf("pushing with mount from %s: %w", fromRepo, err)
				}
				log.G(ctx).Debugf("failed to push with mount from repository %s: %v", fromRepo, err)
			}
			if resp != nil {
				switch resp.StatusCode {
				case http.StatusUnauthorized:
					log.G(ctx).Debugf("failed to mount from repository %s, not authorized", fromRepo)

					resp.Body.Close()
					resp = nil
				case http.StatusCreated:
					mountedFrom = path.Join(p.refspec.Hostname(), fromRepo)
				}
			}
		}

		if resp == nil {
			resp, err = req.doWithRetries(ctx, true)
			if err != nil {
				if errors.Is(err, ErrInvalidAuthorization) {
					return nil, fmt.Errorf("push access denied, repository does not exist or may require authorization: %w", err)
				}
				return nil, err
			}
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		case http.StatusCreated:
			p.tracker.SetStatus(ref, Status{
				Committed: true,
				PushStatus: PushStatus{
					MountedFrom: mountedFrom,
				},
				Status: content.Status{
					Ref:    ref,
					Total:  desc.Size,
					Offset: desc.Size,
				},
			})
			return nil, fmt.Errorf("content %v on remote: %w", desc.Digest, errdefs.ErrAlreadyExists)
		default:
			err := unexpectedResponseErr(resp)
			log.G(ctx).WithError(err).Debug("unexpected response")
			return nil, err
		}

		var (
			location = resp.Header.Get("Location")
			lurl     *url.URL
			lhost    = host
		)
		// Support paths without host in location
		if strings.HasPrefix(location, "/") {
			lurl, err = url.Parse(lhost.Scheme + "://" + lhost.Host + location)
			if err != nil {
				return nil, fmt.Errorf("unable to parse location %v: %w", location, err)
			}
		} else {
			if !strings.Contains(location, "://") {
				location = lhost.Scheme + "://" + location
			}
			lurl, err = url.Parse(location)
			if err != nil {
				return nil, fmt.Errorf("unable to parse location %v: %w", location, err)
			}

			if lurl.Host != lhost.Host || lhost.Scheme != lurl.Scheme {
				lhost.Scheme = lurl.Scheme
				lhost.Host = lurl.Host

				// Check if different than what was requested, accounting for fallback in the transport layer
				requested := resp.Request.URL
				if requested.Host != lhost.Host || requested.Scheme != lhost.Scheme {
					// Strip authorizer if change to host or scheme
					lhost.Authorizer = nil
					log.G(ctx).WithField("host", lhost.Host).WithField("scheme", lhost.Scheme).Debug("upload changed destination, authorizer removed")
				}
			}
		}
		q := lurl.Query()
		q.Add("digest", desc.Digest.String())

		req = p.request(lhost, http.MethodPut)
		req.header.Set("Content-Type", "application/octet-stream")
		req.path = lurl.Path + "?" + q.Encode()
		if err := req.addNamespace(p.refspec.Hostname()); err != nil {
			return nil, err
		}
	}
	p.tracker.SetStatus(ref, Status{
		Status: content.Status{
			Ref:       ref,
			Total:     desc.Size,
			Expected:  desc.Digest,
			StartedAt: time.Now(),
		},
	})

	// TODO: Support chunked upload

	pushw := newPushWriter(p.dockerBase, ref, desc.Digest, p.tracker, isManifest)

	req.body = func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		pushw.setPipe(pw)
		return pr, nil
	}
	req.size = desc.Size

	// Defer sending the PUT request until the caller actually starts writing
	// content (or commits). Issuing the request here would put it on the wire
	// with an idle body, and a reverse proxy in front of the registry may close
	// the connection on an inactivity timeout before any content is streamed
	// (e.g. when many uploads are opened but only a few are fed at a time on a
	// slow uplink). Starting the request lazily ensures the body streams as soon
	// as the request is made, so the connection never sits idle.
	pushw.start = func() {
		go func() {
			resp, err := req.doWithRetries(ctx, true)
			if err != nil {
				pushw.setError(err)
				return
			}

			switch resp.StatusCode {
			case http.StatusOK, http.StatusCreated, http.StatusNoContent:
			default:
				err := unexpectedResponseErr(resp)
				log.G(ctx).WithError(err).Debug("unexpected response")
				pushw.setError(err)
				return
			}
			pushw.setResponse(resp)
		}()
	}

	return pushw, nil
}

func getManifestPath(object string, dgst digest.Digest) []string {
	if i := strings.IndexByte(object, '@'); i >= 0 {
		if object[i+1:] != dgst.String() {
			// use digest, not tag
			object = ""
		} else {
			// strip @<digest> for registry path to make tag
			object = object[:i]
		}

	}

	if object == "" {
		return []string{"manifests", dgst.String()}
	}

	return []string{"manifests", object}
}

type pushWriter struct {
	base *dockerBase
	ref  string

	pipe *io.PipeWriter

	done      chan struct{}
	closeOnce sync.Once

	// start issues the upload request. It is set by push and invoked lazily,
	// exactly once, on the first Write or on Commit, so the request body is not
	// put on the wire until there is content to stream.
	start     func()
	startOnce sync.Once

	pipeC chan *io.PipeWriter
	respC chan *http.Response
	errC  chan error

	isManifest bool

	expected digest.Digest
	tracker  StatusTracker
}

func newPushWriter(db *dockerBase, ref string, expected digest.Digest, tracker StatusTracker, isManifest bool) *pushWriter {
	// Initialize and create response
	return &pushWriter{
		base:       db,
		ref:        ref,
		expected:   expected,
		tracker:    tracker,
		pipeC:      make(chan *io.PipeWriter, 1),
		respC:      make(chan *http.Response, 1),
		errC:       make(chan error, 1),
		done:       make(chan struct{}),
		isManifest: isManifest,
	}
}

// ensureStarted issues the upload request exactly once. It is safe to call
// from both Write and Commit; only the first call has any effect.
func (pw *pushWriter) ensureStarted() {
	pw.startOnce.Do(func() {
		// Don't start the request if the writer has already been closed. The
		// caller checks pw.done before calling ensureStarted, but Close can race
		// in between; checking again here avoids starting a request (and its
		// goroutine/connection) that would immediately be torn down.
		select {
		case <-pw.done:
			return
		default:
		}
		if pw.start != nil {
			pw.start()
		}
	})
}

func (pw *pushWriter) setPipe(p *io.PipeWriter) {
	// If the writer was closed before the request goroutine installed this pipe
	// (e.g. Close raced with ensureStarted), close the discarded pipe writer so
	// the request body reader returns an error instead of blocking forever,
	// which would leak the request goroutine and its connection. Check done with
	// priority: pipeC is buffered, so a plain select could otherwise enqueue the
	// pipe into a slot that no one will drain, leaving it unclosed.
	select {
	case <-pw.done:
		p.CloseWithError(io.ErrClosedPipe)
		return
	default:
	}
	select {
	case <-pw.done:
		p.CloseWithError(io.ErrClosedPipe)
		return
	case pw.pipeC <- p:
	}

	// Close may have closed done after the check above but before Write/Commit
	// adopted the pipe. In that case the receiver can observe done and return
	// without draining pipeC, leaving the pipe stranded and its request body
	// reader blocked forever. Reclaim and close it if it is still buffered; if
	// Write/Commit already adopted it, the receive yields nothing and they own
	// closing it. Close performs the same reclaim, so the two cover each other.
	select {
	case <-pw.done:
		select {
		case orphan := <-pw.pipeC:
			orphan.CloseWithError(io.ErrClosedPipe)
		default:
		}
	default:
	}
}

func (pw *pushWriter) setError(err error) {
	select {
	case <-pw.done:
	case pw.errC <- err:
	}
}

func (pw *pushWriter) setResponse(resp *http.Response) {
	// If the writer was closed before the response was consumed, close the
	// response body so the underlying connection is not leaked. Mirror setPipe:
	// check done with priority (respC is buffered) and reclaim after the send in
	// case Close races in between.
	select {
	case <-pw.done:
		resp.Body.Close()
		return
	default:
	}
	select {
	case <-pw.done:
		resp.Body.Close()
		return
	case pw.respC <- resp:
	}

	select {
	case <-pw.done:
		select {
		case orphan := <-pw.respC:
			orphan.Body.Close()
		default:
		}
	default:
	}
}

func (pw *pushWriter) replacePipe(p *io.PipeWriter) error {
	if pw.pipe == nil {
		pw.pipe = p
		return nil
	}

	pw.pipe.CloseWithError(content.ErrReset)
	pw.pipe = p

	// If content has already been written, the bytes
	// cannot be written again and the caller must reset
	status, err := pw.tracker.GetStatus(pw.ref)
	if err != nil {
		return err
	}
	status.Offset = 0
	status.UpdatedAt = time.Now()
	pw.tracker.SetStatus(pw.ref, status)
	return content.ErrReset
}

func (pw *pushWriter) Write(p []byte) (n int, err error) {
	// A zero-length write is a valid no-op. Don't start the request for it;
	// doing so would reintroduce the idle-body window this change removes.
	if len(p) == 0 {
		return 0, nil
	}

	status, err := pw.tracker.GetStatus(pw.ref)
	if err != nil {
		return n, err
	}

	// Don't start the request if the writer has already been closed. Starting it
	// here would leave the request goroutine blocked reading from a pipe that is
	// never written or closed, leaking the goroutine and its connection.
	select {
	case <-pw.done:
		return 0, io.ErrClosedPipe
	default:
	}

	// Issue the upload request now that there is content to stream. The pipe is
	// delivered by the request goroutine, so this must happen before waiting on
	// pipeC below.
	pw.ensureStarted()

	if pw.pipe == nil {
		select {
		case <-pw.done:
			return 0, io.ErrClosedPipe
		case p := <-pw.pipeC:
			pw.replacePipe(p)
		}
	} else {
		select {
		case <-pw.done:
			return 0, io.ErrClosedPipe
		case p := <-pw.pipeC:
			return 0, pw.replacePipe(p)
		default:
		}
	}

	n, err = pw.pipe.Write(p)
	if errors.Is(err, io.ErrClosedPipe) {
		// if the pipe is closed, we might have the original error on the error
		// channel - so we should try and get it
		select {
		case <-pw.done:
		case err = <-pw.errC:
			pw.Close()
		case p := <-pw.pipeC:
			return 0, pw.replacePipe(p)
		case resp := <-pw.respC:
			pw.setResponse(resp)
		}
	}
	status.Offset += int64(n)
	status.UpdatedAt = time.Now()
	pw.tracker.SetStatus(pw.ref, status)
	return
}

func (pw *pushWriter) Close() error {
	// Ensure pipeC is closed but handle `Close()` being
	// called multiple times without panicking
	pw.closeOnce.Do(func() {
		close(pw.done)
		// Reclaim any pipe setPipe left buffered that Write/Commit will not
		// adopt (they return once done is closed), so the request body reader
		// unblocks instead of leaking the request goroutine and its connection.
		select {
		case p := <-pw.pipeC:
			p.CloseWithError(io.ErrClosedPipe)
		default:
		}
		// Likewise reclaim a buffered response that will never be committed and
		// close its body, otherwise the underlying connection is leaked.
		select {
		case resp := <-pw.respC:
			resp.Body.Close()
		default:
		}
	})
	// Closing an incomplete writer. Record this as an error so that a following
	// push of the same ref can retry it. This also covers a writer that was
	// opened but never fed (pw.pipe == nil): with the request started lazily
	// that is a normal path, and without this the tracker entry would keep
	// looking like an in-progress upload and block later pushes with
	// ErrUnavailable.
	status, err := pw.tracker.GetStatus(pw.ref)
	if err == nil && !status.Committed {
		status.ErrClosed = errors.New("closed incomplete writer")
		pw.tracker.SetStatus(pw.ref, status)
	}
	if pw.pipe != nil {
		return pw.pipe.Close()
	}
	return nil
}

func (pw *pushWriter) Status() (content.Status, error) {
	status, err := pw.tracker.GetStatus(pw.ref)
	if err != nil {
		return content.Status{}, err
	}
	return status.Status, nil

}

func (pw *pushWriter) Digest() digest.Digest {
	// TODO: Get rid of this function?
	return pw.expected
}

func (pw *pushWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	// Don't start the request if the writer has already been closed; starting it
	// would leak the request goroutine and its connection (see Write).
	select {
	case <-pw.done:
		return io.ErrClosedPipe
	default:
	}

	// Ensure the upload request has been issued. For zero-length content Write
	// is never called, so the request must be started here.
	pw.ensureStarted()

	// If Write was never called (e.g. a zero-length blob) the request goroutine
	// still creates a pipe for the request body but it was never adopted. The
	// goroutine enqueues that pipe on pipeC before it produces a response, so
	// always adopt the pipe when it is available, even if a response or error is
	// already queued. Otherwise the response wait below could take its pipeC
	// branch and return early, skipping validation and leaving the pipe
	// unclosed. It is then closed below to signal an empty body.
	if pw.pipe == nil {
		select {
		case p := <-pw.pipeC:
			pw.pipe = p
		case <-pw.done:
			return io.ErrClosedPipe
		case err := <-pw.errC:
			// Reached only if the request failed before the body pipe was created.
			pw.Close()
			return err
		}
	}

	// Check whether read has already thrown an error
	if pw.pipe != nil {
		if _, err := pw.pipe.Write([]byte{}); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return fmt.Errorf("pipe error before commit: %w", err)
		}
		if err := pw.pipe.Close(); err != nil {
			return err
		}
	}

	// TODO: timeout waiting for response
	var resp *http.Response
	select {
	case <-pw.done:
		return io.ErrClosedPipe
	case err := <-pw.errC:
		pw.Close()
		return err
	case resp = <-pw.respC:
		defer resp.Body.Close()
	case p := <-pw.pipeC:
		// check whether the pipe has changed in the commit, because sometimes Write
		// can complete successfully, but the pipe may have changed. In that case, the
		// content needs to be reset.
		return pw.replacePipe(p)
	}

	// 201 is specified return status, some registries return
	// 200, 202 or 204.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusAccepted:
	default:
		return unexpectedResponseErr(resp)
	}

	status, err := pw.tracker.GetStatus(pw.ref)
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	if size > 0 && size != status.Offset {
		return fmt.Errorf("unexpected size %d, expected %d", status.Offset, size)
	}

	if expected == "" {
		expected = status.Expected
	} else if expected != status.Expected {
		return fmt.Errorf("unexpected digest received: got %q, expected %q", status.Expected, expected)
	}

	if dgstHdr := resp.Header.Get("Docker-Content-Digest"); dgstHdr != "" {
		actual, err := digest.Parse(dgstHdr)
		if err != nil {
			return fmt.Errorf("invalid content digest in response: %w", err)
		}

		if actual != expected {
			return fmt.Errorf("got digest %s, expected %s", actual, expected)
		}
	} else {
		log.G(ctx).Info("registry did not send a Docker-Content-Digest header")
	}

	status.Committed = true
	status.UpdatedAt = time.Now()
	pw.tracker.SetStatus(pw.ref, status)

	return nil
}

func (pw *pushWriter) Truncate(size int64) error {
	// TODO: if blob close request and start new request at offset
	// TODO: always error on manifest
	return errors.New("cannot truncate remote upload")
}

// withGETErrorBody enriches originalErr, produced from a bodyless HEAD
// response, with the error body from a follow-up GET to the same URL. HEAD
// responses carry no body, so a 403 only surfaces its status code; a GET
// returns the registry's error details (e.g. "key vault access denied", IP
// restrictions) that explain the failure.
//
// The GET body is only used when the GET also returns 403, so the enriched
// error's status and body stay consistent. In that case the original HEAD
// request's method and status are preserved and only the body is borrowed from
// the GET; any other outcome (GET failed, or returned a different status)
// leaves originalErr untouched.
func withGETErrorBody(ctx context.Context, originalErr error, headResp *http.Response, doGET func() (*http.Response, error)) error {
	getResp, err := doGET()
	if err != nil {
		log.G(ctx).WithError(err).Debug("failed to retrieve error body with GET fallback")
		return originalErr
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusForbidden {
		log.G(ctx).WithFields(log.Fields{
			"head_status": headResp.Status,
			"get_status":  getResp.Status,
		}).Debug("ignoring GET fallback response with different status")
		return originalErr
	}

	// Preserve the original HEAD request's method and status and borrow only
	// the body from the GET, so the error still reflects the request the caller
	// actually made.
	enriched := *headResp
	enriched.Body = getResp.Body
	return unexpectedResponseErr(&enriched)
}

func requestWithMountFrom(req *request, mount, from string) *request {
	creq := *req

	sep := "?"
	if strings.Contains(creq.path, sep) {
		sep = "&"
	}

	creq.path = creq.path + sep + "mount=" + mount + "&from=" + from

	return &creq
}
