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
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"

	"github.com/containerd/containerd/v2/core/transfer"
)

type writeFunc func(p []byte) (int, error)

func (f writeFunc) Write(p []byte) (int, error) { return f(p) }

func TestFetcherOpen(t *testing.T) {
	content := make([]byte, 128)
	rand.New(rand.NewSource(1)).Read(content)
	start := 0

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if start > 0 {
			rw.Header().Set("content-range", fmt.Sprintf("bytes %d-127/128", start))
		} else if r.Header.Get("Range") == "bytes=0-" {
			// Simulate registries which do not support range requests
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		rw.Header().Set("content-length", strconv.Itoa(len(content[start:])))
		_, _ = rw.Write(content[start:])
	}))
	defer s.Close()

	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := dockerFetcher{&dockerBase{
		repository: "nonempty",
	}}

	host := RegistryHost{
		Client: s.Client(),
		Host:   u.Host,
		Scheme: u.Scheme,
		Path:   u.Path,
	}

	ctx := context.Background()

	req := f.request(host, http.MethodGet)

	checkReader := func(o int64) {
		t.Helper()

		rc, _, err := f.open(ctx, req, "", o, true)
		if err != nil {
			t.Fatalf("failed to open: %+v", err)
		}
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		expected := content[o:]
		if len(b) != len(expected) {
			t.Errorf("unexpected length %d, expected %d", len(b), len(expected))
			return
		}
		for i, c := range expected {
			if b[i] != c {
				t.Errorf("unexpected byte %x at %d, expected %x", b[i], i, c)
				return
			}
		}

	}

	checkReader(0)

	// Test server ignores content range
	checkReader(25)

	// Use content range on server
	start = 20
	checkReader(20)

	// Check returning just last byte and no bytes
	start = 127
	checkReader(127)
	start = 128
	checkReader(128)

	// Check that server returning a different content range
	// then requested errors
	start = 30
	_, _, err = f.open(ctx, req, "", 20, true)
	if err == nil {
		t.Fatal("expected error opening with invalid server response")
	}
}

func TestFetcherOpenParallel(t *testing.T) {
	size := int64(3 * 1024 * 1024)
	content := make([]byte, size)
	rand.New(rand.NewSource(1)).Read(content)
	sendContentLength := true

	f := dockerFetcher{
		&dockerBase{
			repository: "nonempty",
			limiter:    nil,
			performances: transfer.ImageResolverPerformanceSettings{
				ConcurrentLayerFetchBuffer: 0,
			},
		},
	}

	cfgConcurency := func(maxConcurrentDls, chunkSize int) {
		f.dockerBase.performances.MaxConcurrentDownloads = maxConcurrentDls
		f.dockerBase.performances.ConcurrentLayerFetchBuffer = chunkSize
		limiter := semaphore.NewWeighted(int64(maxConcurrentDls))
		f.dockerBase.limiter = limiter
	}

	ignoreContentRange := false
	var failTimeoutOnceAfter atomic.Int64
	failAfter := int64(0)
	var forceRange []httpRange
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rng, err := parseRange(r.Header.Get("Range"), size)
		if errors.Is(err, errNoOverlap) {
			err = nil
		}
		assert.NoError(t, err)
		if ignoreContentRange {
			rng = nil
		}
		if forceRange != nil {
			rng = forceRange
		}
		if failAfter > 0 && rng[0].start > failAfter {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		timeoutOnceAfter := failTimeoutOnceAfter.Load()
		if timeoutOnceAfter > 0 && rng[0].start > timeoutOnceAfter {
			failTimeoutOnceAfter.Store(0)
			rw.WriteHeader(http.StatusRequestTimeout)
			return
		}

		if len(rng) == 0 {
			if sendContentLength {
				rw.Header().Set("content-length", strconv.Itoa(len(content)))
			}
			_, _ = rw.Write(content)
		} else {
			b := content[rng[0].start : rng[0].start+rng[0].length]
			rw.Header().Set("content-range", rng[0].contentRange(size))
			if sendContentLength {
				rw.Header().Set("content-length", strconv.Itoa(len(b)))
			}
			_, _ = rw.Write(b)
		}

	}))
	defer s.Close()

	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}

	host := RegistryHost{
		Client: s.Client(),
		Host:   u.Host,
		Scheme: u.Scheme,
		Path:   u.Path,
	}

	ctx := context.Background()

	req := f.request(host, http.MethodGet)

	checkReader := func(offset int64) {
		t.Helper()

		rc, _, err := f.open(ctx, req, "", offset, true)
		if err != nil {
			t.Fatalf("failed to open: %+v", err)
		}
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		expected := content[offset:]
		if len(b) != len(expected) {
			t.Errorf("unexpected length %d, expected %d", len(b), len(expected))
			return
		}
		for i, c := range expected {
			if b[i] != c {
				t.Errorf("unexpected byte %x at %d, expected %x", b[i], i, c)
				return
			}
		}
		assert.NoError(t, rc.Close())
	}

	cfgConcurency(10, 1*1024*1024)
	checkReader(0)

	checkReader(25)

	sendContentLength = false
	checkReader(25)
	sendContentLength = true

	ignoreContentRange = true
	checkReader(0)
	checkReader(25)
	ignoreContentRange = false

	//Use content range on server
	checkReader(20)

	//Check returning just last byte and no bytes
	checkReader(size - 1)
	checkReader(size)

	// Check that server returning a different content range
	// than requested errors
	forceRange = []httpRange{{start: 10, length: size - 20}}
	_, _, err = f.open(ctx, req, "", 20, true)
	if err == nil {
		t.Fatal("expected error opening with invalid server response")
	}
	forceRange = nil

	// check retrying a request in the middle
	failTimeoutOnceAfter.Store(1 * 1024 * 1024)
	checkReader(20)

	failAfter = 1
	forceRange = []httpRange{{start: 20}}
	_, _, err = f.open(ctx, req, "", 20, true)
	assert.ErrorContains(t, err, "unexpected status")
	forceRange = nil
	failAfter = 0

	// test a case when a subsequent request fails and shouldn't have
	failAfter = 1 * 1024 * 1024
	body, _, err := f.open(ctx, req, "", 0, true)
	assert.NoError(t, err)
	_, err = io.ReadAll(body)
	assert.Error(t, err, "this should have failed")
}

func TestFetcherOpenParallel_CloseAfterCopyError(t *testing.T) {
	size := int64(10 * 1024 * 1024)
	content := make([]byte, size)
	rr, err := rand.New(rand.NewSource(1)).Read(content)
	require.NoError(t, err)
	require.Equal(t, int(size), rr)

	type errWriter struct {
		max int64
		n   int64
	}

	ew := &errWriter{max: 1024}
	ewWrite := func(p []byte) (int, error) {
		n := len(p)
		ew.n += int64(n)
		if ew.n >= ew.max {
			return 0, errors.New("simulated write failure after limit reached")
		}
		return n, nil
	}

	// simulate Close should not wait for download to complete after write error
	unblockOnce := sync.Once{}
	unblock := make(chan struct{})
	unblockAll := func() { unblockOnce.Do(func() { close(unblock) }) }
	defer unblockAll()

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rng, err := parseRange(r.Header.Get("Range"), size)
		if errors.Is(err, errNoOverlap) {
			err = nil
		}
		assert.NoError(t, err)
		if len(rng) == 0 {
			rw.Header().Set("content-length", strconv.Itoa(len(content)))
			_, _ = rw.Write(content)
			return
		}
		if rng[0].start > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-unblock:
			}
		}
		b := content[rng[0].start : rng[0].start+rng[0].length]
		rw.Header().Set("content-range", rng[0].contentRange(size))
		rw.Header().Set("content-length", strconv.Itoa(len(b)))
		_, err = rw.Write(b)
		t.Logf("wrote range %s, err=%v", rng[0].contentRange(size), err)
	}))
	defer s.Close()

	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := dockerFetcher{
		&dockerBase{
			repository: "nonempty",
			limiter:    semaphore.NewWeighted(4),
			performances: transfer.ImageResolverPerformanceSettings{
				MaxConcurrentDownloads:     4,
				ConcurrentLayerFetchBuffer: 1 * 1024 * 1024,
			},
		},
	}

	host := RegistryHost{
		Client: s.Client(),
		Host:   u.Host,
		Scheme: u.Scheme,
		Path:   u.Path,
	}

	req := f.request(host, http.MethodGet)
	rc, _, err := f.open(context.Background(), req, "", 0, true)
	require.NoError(t, err, "failed to open reader")

	_, copyErr := io.Copy(writeFunc(ewWrite), rc)
	require.NotNil(t, copyErr, "expected write error during copy")

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rc.Close()
	}()

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("close error: %v", err)
		}
	case <-timer.C:
		t.Errorf("close blocked after write error")
		unblockAll()
		<-closeDone
	}
}

func TestContentEncoding(t *testing.T) {
	t.Parallel()

	zstdEncode := func(in []byte) []byte {
		var b bytes.Buffer
		zw, err := zstd.NewWriter(&b)
		if err != nil {
			t.Fatal(err)
		}
		_, err = zw.Write(in)
		if err != nil {
			t.Fatal()
		}
		err = zw.Close()
		if err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	gzipEncode := func(in []byte) []byte {
		var b bytes.Buffer
		gw := gzip.NewWriter(&b)
		_, err := gw.Write(in)
		if err != nil {
			t.Fatal(err)
		}
		err = gw.Close()
		if err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	flateEncode := func(in []byte) []byte {
		var b bytes.Buffer
		dw, err := flate.NewWriter(&b, -1)
		if err != nil {
			t.Fatal(err)
		}
		_, err = dw.Write(in)
		if err != nil {
			t.Fatal(err)
		}
		err = dw.Close()
		if err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}

	tests := []struct {
		encodingFuncs  []func([]byte) []byte
		encodingHeader string
	}{
		{
			encodingFuncs:  []func([]byte) []byte{},
			encodingHeader: "",
		},
		{
			encodingFuncs:  []func([]byte) []byte{zstdEncode},
			encodingHeader: "zstd",
		},
		{
			encodingFuncs:  []func([]byte) []byte{gzipEncode},
			encodingHeader: "gzip",
		},
		{
			encodingFuncs:  []func([]byte) []byte{flateEncode},
			encodingHeader: "deflate",
		},
		{
			encodingFuncs:  []func([]byte) []byte{zstdEncode, gzipEncode},
			encodingHeader: "zstd,gzip",
		},
		{
			encodingFuncs:  []func([]byte) []byte{gzipEncode, flateEncode},
			encodingHeader: "gzip,deflate",
		},
		{
			encodingFuncs:  []func([]byte) []byte{gzipEncode, zstdEncode},
			encodingHeader: "gzip,zstd",
		},
		{
			encodingFuncs:  []func([]byte) []byte{gzipEncode, zstdEncode, flateEncode},
			encodingHeader: "gzip,zstd,deflate",
		},
	}

	for _, tc := range tests {
		t.Run(tc.encodingHeader, func(t *testing.T) {
			t.Parallel()
			content := make([]byte, 128)
			rand.New(rand.NewSource(1)).Read(content)

			s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				compressedContent := content
				for _, enc := range tc.encodingFuncs {
					compressedContent = enc(compressedContent)
				}
				rw.Header().Set("content-length", fmt.Sprintf("%d", len(compressedContent)))
				rw.Header().Set("Content-Encoding", tc.encodingHeader)
				rw.Write(compressedContent)
			}))
			defer s.Close()

			u, err := url.Parse(s.URL)
			if err != nil {
				t.Fatal(err)
			}

			f := dockerFetcher{&dockerBase{
				repository: "nonempty",
			}}

			host := RegistryHost{
				Client: s.Client(),
				Host:   u.Host,
				Scheme: u.Scheme,
				Path:   u.Path,
			}

			req := f.request(host, http.MethodGet)

			rc, _, err := f.open(context.Background(), req, "", 0, true)
			if err != nil {
				t.Fatalf("failed to open for encoding %s: %+v", tc.encodingHeader, err)
			}
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			expected := content
			if len(b) != len(expected) {
				t.Errorf("unexpected length %d, expected %d", len(b), len(expected))
				return
			}
			for i, c := range expected {
				if b[i] != c {
					t.Errorf("unexpected byte %x at %d, expected %x", b[i], i, c)
					return
				}
			}
		})
	}
}

// New set of tests to test new error cases
func TestDockerFetcherOpen(t *testing.T) {
	gmt, err := time.LoadLocation("GMT")
	if err != nil {
		t.Fatalf("%q", err.Error())
	}

	type mockedResponse struct {
		mockedHeaders http.Header
		mockedStatus  int
		mockedErr     error
	}

	tests := []struct {
		name                   string
		want                   io.ReadCloser
		wantErr                bool
		wantServerMessageError bool
		wantPlainError         bool
		retries                int
		lastHost               bool

		mockedResponses   []mockedResponse
		executionDuration time.Duration
		executionTime     time.Time
	}{
		// {
		// 	name: "should return status and error.message if it exists if the registry request fails",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus: http.StatusInternalServerError,
		// 			mockedErr: Errors{Error{
		// 				Code:    ErrorCodeUnknown,
		// 				Message: "Test Error",
		// 			}},
		// 		},
		// 	},
		// 	want:                   nil,
		// 	wantErr:                true,
		// 	wantServerMessageError: true,
		// 	lastHost:               false,
		// },
		// {
		// 	name: "should return just status if the registry request fails and does not return a docker error",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus: http.StatusInternalServerError,
		// 			mockedErr:    errors.New("Non-docker error"),
		// 		},
		// 	},
		// 	want:           nil,
		// 	wantErr:        true,
		// 	wantPlainError: true,
		// 	lastHost:       false,
		// },
		// {
		// 	name: "should return StatusRequestTimeout after 5 retries",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus: http.StatusRequestTimeout,
		// 			mockedErr:    errors.New(http.StatusText(http.StatusRequestTimeout)),
		// 		},
		// 	},
		// 	want:           nil,
		// 	wantErr:        true,
		// 	wantPlainError: true,
		// 	retries:        5,
		// 	lastHost:       true,
		// },
		// {
		// 	name: "should retry once after 500",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus: http.StatusInternalServerError,
		// 			mockedErr: Errors{Error{
		// 				Code:    ErrorCodeUnknown,
		// 				Message: "Test Error",
		// 			}},
		// 		},
		// 	},
		// 	want:                   nil,
		// 	wantErr:                true,
		// 	wantServerMessageError: true,
		// 	lastHost:               true,
		// 	retries:                1,
		// },
		// {
		// 	name: "should retry once after 504",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus: http.StatusGatewayTimeout,
		// 			mockedErr: Errors{Error{
		// 				Code:    ErrorCodeUnknown,
		// 				Message: "Test Error",
		// 			}},
		// 		},
		// 	},
		// 	want:                   nil,
		// 	wantErr:                true,
		// 	wantServerMessageError: true,
		// 	lastHost:               true,
		// 	retries:                1,
		// },
		{
			name: "should retry after specified date time in `Retry-After` response header",
			mockedResponses: []mockedResponse{
				{
					mockedStatus:  http.StatusTooManyRequests,
					mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
					mockedHeaders: http.Header{"Retry-After": {time.Now().Add(time.Second * 3).In(gmt).Format("Mon, 02 Jan 2006 15:04:05 GMT")}},
				},
				{
					mockedStatus: http.StatusOK,
					mockedErr:    nil,
				},
			},
			want:          io.NopCloser(strings.NewReader("Ok")),
			wantErr:       false,
			executionTime: time.Now().Add(time.Second * 3),
			lastHost:      true,
			retries:       1,
		},
		// {
		// 	name: "should retry after a delay if `Retry-After` response header is missing",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus: http.StatusTooManyRequests,
		// 			mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
		// 		},
		// 		{
		// 			mockedStatus: http.StatusTooManyRequests,
		// 			mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
		// 		},
		// 		{
		// 			mockedStatus: http.StatusOK,
		// 			mockedErr:    nil,
		// 		},
		// 	},
		// 	retries:           2,
		// 	executionDuration: time.Second * 3,
		// 	lastHost:          true,
		// 	wantErr:           false,
		// },
		// {
		// 	name: "should retry after specified seconds in `Retry-After` response header",
		// 	mockedResponses: []mockedResponse{
		// 		{
		// 			mockedStatus:  http.StatusTooManyRequests,
		// 			mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
		// 			mockedHeaders: http.Header{"Retry-After": {"1"}},
		// 		},
		// 		{
		// 			mockedStatus: http.StatusOK,
		// 			mockedErr:    nil,
		// 		},
		// 	},
		// 	executionDuration: time.Second * 1,
		// 	retries:           1,
		// 	lastHost:          true,
		// 	wantErr:           false,
		// },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestCount := 0
			s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if requestCount != 0 {
					tt.retries--
				}

				var mock mockedResponse
				if requestCount >= len(tt.mockedResponses) {
					mock = tt.mockedResponses[len(tt.mockedResponses)-1]
				} else {
					mock = tt.mockedResponses[requestCount]
				}

				for key, values := range mock.mockedHeaders {
					for _, value := range values {
						rw.Header().Add(key, value)
					}
				}

				rw.WriteHeader(mock.mockedStatus)
				if mock.mockedErr != nil {
					bytes, _ := json.Marshal(mock.mockedErr)
					rw.Write(bytes)
				}
				requestCount++
			}))
			defer s.Close()

			u, err := url.Parse(s.URL)
			if err != nil {
				t.Fatal(err)
			}

			f := dockerFetcher{&dockerBase{
				repository: "ns",
			}}

			host := RegistryHost{
				Client: s.Client(),
				Host:   u.Host,
				Scheme: u.Scheme,
				Path:   u.Path,
			}

			want := time.Now()
			if !tt.executionTime.IsZero() {
				want = tt.executionTime
			} else {
				want = want.Add(tt.executionDuration)
			}

			req := f.request(host, http.MethodGet)
			got, _, err := f.open(context.TODO(), req, "", 0, tt.lastHost)
			data, err := io.ReadAll(got)
			defer got.Close()
			fmt.Println("GOT: ", data)
			fmt.Println("GOT err : ", err)

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, 0, tt.retries)
			if !tt.executionTime.IsZero() || tt.executionDuration > 0 {
				assert.GreaterOrEqual(t, time.Now().Truncate(time.Second), want.Truncate(time.Second))
			}

			if tt.wantErr {
				mock := tt.mockedResponses[len(tt.mockedResponses)-1]
				expectedError := fmt.Sprintf("unexpected status from GET request to %s/ns: %v %s", s.URL, mock.mockedStatus, http.StatusText(mock.mockedStatus))
				if tt.wantServerMessageError {
					expectedError += "\n" + mock.mockedErr.Error()
				}
				assert.Equal(t, expectedError, err.Error())
			}
		})
	}
}

// func TestDockerFetcherOpenError429(t *testing.T) {
// 	gmt, err := time.LoadLocation("GMT")
// 	if err != nil {
// 		t.Fatalf("%q", err.Error())
// 	}
//
// 	type mockedResponse struct {
// 		mockedHeaders http.Header
// 		mockedStatus  int
// 		mockedErr     error
// 	}
//
// 	tests := []struct {
// 		name                   string
// 		mockedResponses        []mockedResponse
// 		retries                int
// 		executionDuration      time.Duration
// 		executionTime          time.Time
// 		lastHost               bool
// 		wantErr                bool
// 		wantServerMessageError bool
// 		wantPlainError         bool
// 	}{
// 		{
// 			name: "should retry after specified date time in `Retry-After` response header",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus:  http.StatusTooManyRequests,
// 					mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
// 					mockedHeaders: http.Header{"Retry-After": {time.Now().Add(time.Second * 3).In(gmt).Format("Mon, 02 Jan 2006 15:04:05 GMT")}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
// 			executionTime:  time.Now().Add(time.Second * 3),
// 			retries:        1,
// 			lastHost:       true,
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 		{
// 			name: "should retry after a delay if `Retry-After` response header is missing",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
// 				},
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
// 			retries:           2,
// 			executionDuration: time.Second * 3,
// 			lastHost:          true,
// 			wantErr:           false,
// 			wantPlainError:    false,
// 		},
// 		{
// 			name: "should retry after specified seconds in `Retry-After` response header",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus:  http.StatusTooManyRequests,
// 					mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
// 					mockedHeaders: http.Header{"Retry-After": {"1"}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
// 			executionDuration: time.Second * 1,
// 			retries:           1,
// 			lastHost:          true,
// 			wantErr:           false,
// 			wantPlainError:    false,
// 		},
// 	}
//
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			requestCount := 0
// 			s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
// 				if requestCount != 0 {
// 					tt.retries--
// 				}
//
// 				var mock mockedResponse
// 				if requestCount >= len(tt.mockedResponses) {
// 					mock = tt.mockedResponses[len(tt.mockedResponses)-1]
// 				} else {
// 					mock = tt.mockedResponses[requestCount]
// 				}
//
// 				for key, values := range mock.mockedHeaders {
// 					for _, value := range values {
// 						rw.Header().Add(key, value)
// 					}
// 				}
//
// 				rw.WriteHeader(mock.mockedStatus)
//
// 				if mock.mockedErr != nil {
// 					body, _ := json.Marshal(mock.mockedErr)
// 					rw.Write(body)
// 				}
//
// 				requestCount++
// 			}))
// 			defer s.Close()
//
// 			u, err := url.Parse(s.URL)
// 			if err != nil {
// 				t.Fatal(err)
// 			}
//
// 			f := dockerFetcher{&dockerBase{
// 				repository: "ns",
// 			}}
//
// 			host := RegistryHost{
// 				Client: s.Client(),
// 				Host:   u.Host,
// 				Scheme: u.Scheme,
// 				Path:   u.Path,
// 			}
//
// 			want := time.Now()
// 			if !tt.executionTime.IsZero() {
// 				want = tt.executionTime
// 			} else {
// 				want = want.Add(tt.executionDuration)
// 			}
//
// 			req := f.request(host, http.MethodGet)
// 			_, _, err = f.open(context.TODO(), req, "", 0, tt.lastHost)
//
// 			if !tt.executionTime.IsZero() || tt.executionDuration > 0 {
// 				assert.GreaterOrEqual(t, time.Now().Truncate(time.Second), want.Truncate(time.Second))
// 			}
// 			assert.Equal(t, tt.wantErr, err != nil)
// 			assert.Equal(t, 0, tt.retries)
//
// 			if tt.wantErr {
// 				mock := tt.mockedResponses[len(tt.mockedResponses)-1]
// 				expectedError := fmt.Sprintf("unexpected status from GET request to %s/ns: %v %s", s.URL, mock.mockedStatus, http.StatusText(mock.mockedStatus))
// 				if tt.wantServerMessageError {
// 					expectedError += "\n" + mock.mockedErr.Error()
// 				}
// 				assert.Equal(t, expectedError, err.Error())
// 			}
// 		})
// 	}
// }

// type roundTripperFunc func(*http.Request) (*http.Response, error)
//
// func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
// 	return fn(r)
// }
//
// func TestDockerFetcherOpenError429(t *testing.T) {
// 	gmt, err := time.LoadLocation("GMT")
// 	if err != nil {
// 		t.Fatalf("%q", err.Error())
// 	}
//
// 	type retryAfter struct {
// 		duration time.Duration
// 		asDate   bool
// 	}
//
// 	type mockedResponse struct {
// 		mockedHeaders http.Header
// 		mockedStatus  int
// 		mockedErr     error
// 	}
// 	fmt.Println("START: ", time.Now())
// 	tests := []struct {
// 		name                   string
// 		mockedResponses        []mockedResponse
// 		retries                int
// 		executionDuration      time.Duration
// 		executionTime          time.Time
// 		lastHost               bool
// 		wantErr                bool
// 		wantServerMessageError bool
// 		wantPlainError         bool
// 	}{
// 		{
// 			name: "should retry after specified date time in `Retry-After` response header",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
//
// 					mockedHeaders: http.Header{"Retry-After": {time.Now().Add(time.Duration(time.Second * 3)).In(gmt).Format("Mon, 02 Jan 2006 15:04:05 GMT")}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
//
// 			executionTime: time.Now().Add(time.Duration(time.Second * 3)),
// 			retries:       1,
// 			lastHost:      true,
//
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 		{
// 			name: "should retry after a delay if `Retry-After` response header is missing",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
// 				},
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
// 			retries: 2,
// 			// Based on backoff
// 			// 1s -> 2s -> 4s -> 8s -> 16s
// 			executionDuration: time.Second * 3,
// 			lastHost:          true,
//
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 		{
// 			name: "should retry after specified seconds in `Retry-After` response header",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus:  http.StatusTooManyRequests,
// 					mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
// 					mockedHeaders: http.Header{"Retry-After": {"1"}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
//
// 			executionDuration: time.Second * 1,
// 			retries:           1,
// 			lastHost:          true,
//
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			requestCount := 0
//
// 			client := &http.Client{
// 				Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
// 					if requestCount != 0 {
// 						tt.retries--
// 					}
//
// 					var body []byte
// 					var mock mockedResponse
// 					if requestCount >= len(tt.mockedResponses) {
// 						mock = tt.mockedResponses[0]
// 					} else {
// 						mock = tt.mockedResponses[requestCount]
// 					}
// 					if mock.mockedErr != nil {
// 						body, _ = json.Marshal(mock.mockedErr)
// 					}
//
// 					resp := &http.Response{
// 						StatusCode: mock.mockedStatus,
// 						Status:     fmt.Sprintf("%d %s", mock.mockedStatus, http.StatusText(mock.mockedStatus)),
// 						Header:     make(http.Header, 1),
// 						Body:       io.NopCloser(bytes.NewReader(body)),
// 						Request:    req,
// 					}
//
// 					for key, values := range mock.mockedHeaders {
// 						for _, value := range values {
// 							resp.Header.Add(key, value)
// 						}
// 					}
//
// 					requestCount++
// 					return resp, nil
// 				}),
// 			}
//
// 			f := dockerFetcher{&dockerBase{
// 				repository: "ns",
// 			}}
//
// 			h := "containerd.io"
// 			s := "http"
// 			host := RegistryHost{
// 				Client: client,
// 				Host:   h,
// 				Scheme: s,
// 				Path:   "",
// 			}
//
// 			want := time.Now()
// 			if !tt.executionTime.IsZero() {
// 				want = tt.executionTime
// 			} else {
// 				want = want.Add(tt.executionDuration)
// 			}
// 			req := f.request(host, http.MethodGet)
//
// 			_, _, err := f.open(context.TODO(), req, "", 0, tt.lastHost)
//
// 			assert.GreaterOrEqual(t, time.Now().Truncate(time.Second), want.Truncate(time.Second))
// 			assert.Equal(t, tt.wantErr, err != nil)
// 			assert.Equal(t, 0, tt.retries)
// 			if tt.wantErr {
// 				mock := tt.mockedResponses[len(tt.mockedResponses)-1]
// 				expectedError := fmt.Sprintf("unexpected status from GET request to %s/ns: %v %s", fmt.Sprintf("%s://%s", s, h), mock.mockedStatus, http.StatusText(mock.mockedStatus))
// 				if tt.wantServerMessageError {
// 					expectedError += "\n" + mock.mockedErr.Error()
// 				}
// 				assert.Equal(t, expectedError, err.Error())
// 			}
// 		})
// 	}
// }

// sync test

// func TestDockerFetcherOpenError429(t *testing.T) {
// 	type mockedResponse struct {
// 		mockedHeaders http.Header
// 		mockedStatus  int
// 		mockedErr     error
// 	}
// 	tests := []struct {
// 		name                   string
// 		mockedResponses        []mockedResponse
// 		retries                int
// 		timeToExecute          time.Duration
// 		lastHost               bool
// 		wantErr                bool
// 		wantServerMessageError bool
// 		wantPlainError         bool
// 	}{
// 		{
// 			name: "should retry after a delay if `Retry-After` response header is missing",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
// 				},
// 			},
// 			retries: 5,
// 			// Based on backoff
// 			// 1s -> 2s -> 4s -> 8s -> 16s
// 			timeToExecute: 31,
// 			lastHost:      true,
//
// 			wantErr:        true,
// 			wantPlainError: true,
// 		},
// 		{
// 			name: "should retry after specified seconds in `Retry-After` response header",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus:  http.StatusTooManyRequests,
// 					mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
// 					mockedHeaders: http.Header{"Retry-After": {"3"}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
//
// 			timeToExecute: 3,
// 			retries:       1,
// 			lastHost:      true,
//
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 		{
// 			name: "should cap delay to 20 seconds",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus:  http.StatusTooManyRequests,
// 					mockedErr:     errors.New(http.StatusText(http.StatusTooManyRequests)),
// 					mockedHeaders: http.Header{"Retry-After": {"30"}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
//
// 			timeToExecute: 20,
// 			retries:       1,
// 			lastHost:      true,
//
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 		{
// 			name: "should retry after specified date time in `Retry-After` response header",
// 			mockedResponses: []mockedResponse{
// 				{
// 					mockedStatus: http.StatusTooManyRequests,
// 					mockedErr:    errors.New(http.StatusText(http.StatusTooManyRequests)),
// 					// In synctest bubble, initial time is at midnight UTC 2000-01-01
// 					// docs: https://pkg.go.dev/testing/synctest#hdr-Time
// 					mockedHeaders: http.Header{"Retry-After": {"Sat, 01 Jan 2000 00:00:08 GMT"}},
// 				},
// 				{
// 					mockedStatus: http.StatusOK,
// 					mockedErr:    nil,
// 				},
// 			},
//
// 			timeToExecute: 8,
// 			retries:       1,
// 			lastHost:      true,
//
// 			wantErr:        false,
// 			wantPlainError: false,
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			synctest.Test(t, func(t *testing.T) {
// 				requestCount := 0
//
// 				client := &http.Client{
// 					Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
// 						if requestCount != 0 {
// 							tt.retries--
// 						}
//
// 						var body []byte
// 						var mock mockedResponse
// 						if requestCount >= len(tt.mockedResponses) {
// 							mock = tt.mockedResponses[0]
// 						} else {
// 							mock = tt.mockedResponses[requestCount]
// 						}
// 						if mock.mockedErr != nil {
// 							body, _ = json.Marshal(mock.mockedErr)
// 						}
//
// 						resp := &http.Response{
// 							StatusCode: mock.mockedStatus,
// 							Status:     fmt.Sprintf("%d %s", mock.mockedStatus, http.StatusText(mock.mockedStatus)),
// 							Header:     make(http.Header, 1),
// 							Body:       io.NopCloser(bytes.NewReader(body)),
// 							Request:    req,
// 						}
//
// 						for key, values := range mock.mockedHeaders {
// 							for _, value := range values {
// 								resp.Header.Add(key, value)
// 							}
// 						}
//
// 						requestCount++
// 						return resp, nil
// 					}),
// 				}
//
// 				f := dockerFetcher{&dockerBase{
// 					repository: "ns",
// 				}}
//
// 				h := "test.com"
// 				s := "http"
// 				host := RegistryHost{
// 					Client: client,
// 					Host:   h,
// 					Scheme: s,
// 					Path:   "",
// 				}
//
// 				want := time.Now().Add(tt.timeToExecute * time.Second)
// 				req := f.request(host, http.MethodGet)
//
// 				_, _, err := f.open(context.TODO(), req, "", 0, tt.lastHost)
//
// 				assert.Equal(t, want, time.Now())
// 				assert.Equal(t, tt.wantErr, err != nil)
// 				assert.Equal(t, 0, tt.retries)
// 				if tt.wantErr {
// 					mock := tt.mockedResponses[len(tt.mockedResponses)-1]
// 					expectedError := fmt.Sprintf("unexpected status from GET request to %s/ns: %v %s", fmt.Sprintf("%s://%s", s, h), mock.mockedStatus, http.StatusText(mock.mockedStatus))
// 					if tt.wantServerMessageError {
// 						expectedError += "\n" + mock.mockedErr.Error()
// 					}
// 					assert.Equal(t, expectedError, err.Error())
// 				}
// 			})
// 		})
// 	}
// }

func TestDockerFetcherOpenLimiterDeadlock(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Encoding", "gzip")
		rw.Write([]byte("bad gzip data"))
		rw.WriteHeader(http.StatusOK)
	}))
	defer s.Close()

	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := dockerFetcher{&dockerBase{
		repository: "ns",
		limiter:    semaphore.NewWeighted(int64(1)),
	}}

	host := RegistryHost{
		Client: s.Client(),
		Host:   u.Host,
		Scheme: u.Scheme,
		Path:   u.Path,
	}

	req := f.request(host, http.MethodGet)
	_, _, err = f.open(context.Background(), req, "", 0, true)
	assert.Error(t, err)

	// verify that the limiter Release has been successfully called when the last open error occurred
	_, _, err = f.open(context.Background(), req, "", 0, true)
	assert.Error(t, err)
}

// httpRange specifies the byte range to be sent to the client.
type httpRange struct {
	start, length int64
}

func (r httpRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.start+r.length-1, size)
}

var (
	errNoOverlap = errors.New("no overlap")
)

// parseRange parses a Range header string as per RFC 7233.
// errNoOverlap is returned if none of the ranges overlap.
func parseRange(s string, size int64) ([]httpRange, error) {
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}
	var ranges []httpRange
	noOverlap := false
	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = textproto.TrimString(ra)
		if ra == "" {
			continue
		}
		i := strings.Index(ra, "-")
		if i < 0 {
			return nil, errors.New("invalid range")
		}
		start, end := textproto.TrimString(ra[:i]), textproto.TrimString(ra[i+1:])
		var r httpRange
		if start == "" {
			// If no start is specified, end specifies the
			// range start relative to the end of the file.
			i, err := strconv.ParseInt(end, 10, 64)
			if err != nil {
				return nil, errors.New("invalid range")
			}
			if i > size {
				i = size
			}
			r.start = size - i
			r.length = size - r.start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i < 0 {
				return nil, errors.New("invalid range")
			}
			if i >= size {
				// If the range begins after the size of the content,
				// then it does not overlap.
				noOverlap = true
				continue
			}
			r.start = i
			if end == "" {
				// If no end is specified, range extends to end of the file.
				r.length = size - r.start
			} else {
				i, err := strconv.ParseInt(end, 10, 64)
				if err != nil || r.start > i {
					return nil, errors.New("invalid range")
				}
				if i >= size {
					i = size - 1
				}
				r.length = i - r.start + 1
			}
		}
		ranges = append(ranges, r)
	}
	if noOverlap && len(ranges) == 0 {
		// The specified ranges did not overlap with the content.
		return nil, errNoOverlap
	}
	return ranges, nil
}
