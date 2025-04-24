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
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/semaphore"
)

func TestFetcherOpen(t *testing.T) {
	content := make([]byte, 128)
	rand.New(rand.NewSource(1)).Read(content)
	start := 0

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if start > 0 {
			rw.Header().Set("content-range", fmt.Sprintf("bytes %d-127/128", start))
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

		rc, err := f.open(ctx, req, "", o)
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
	_, err = f.open(ctx, req, "", 20)
	if err == nil {
		t.Fatal("expected error opening with invalid server response")
	}
}

func TestFetcherOpenParallel(t *testing.T) {
	size := int64(3 * 1024 * 1024)
	content := make([]byte, size)
	rand.New(rand.NewSource(1)).Read(content)

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
	failTimeoutOnceAfter := int64(0)
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
		timeoutOnceAfter := atomic.LoadInt64(&failTimeoutOnceAfter)
		if timeoutOnceAfter > 0 && rng[0].start > timeoutOnceAfter {
			atomic.StoreInt64(&failTimeoutOnceAfter, 0)
			rw.WriteHeader(http.StatusRequestTimeout)
			return
		}

		if len(rng) == 0 {
			rw.Header().Set("content-length", strconv.Itoa(len(content)))
			_, _ = rw.Write(content)
		} else {
			b := content[rng[0].start : rng[0].start+rng[0].length]
			rw.Header().Set("content-range", rng[0].contentRange(size))
			rw.Header().Set("content-length", strconv.Itoa(len(b)))
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

		rc, err := f.open(ctx, req, "", offset)
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

	ignoreContentRange = true
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
	_, err = f.open(ctx, req, "", 20)
	if err == nil {
		t.Fatal("expected error opening with invalid server response")
	}
	forceRange = nil

	// check retrying a request in the middle
	atomic.StoreInt64(&failTimeoutOnceAfter, 1*1024*1024)
	checkReader(20)

	failAfter = 1
	forceRange = []httpRange{{start: 20}}
	_, err = f.open(ctx, req, "", 20)
	assert.ErrorContains(t, err, "unexpected status")
	forceRange = nil
	failAfter = 0

	// test a case when a subsequent request fails and shouldn't have
	failAfter = 1 * 1024 * 1024
	body, err := f.open(ctx, req, "", 0)
	assert.NoError(t, err)
	_, err = io.ReadAll(body)
	assert.Error(t, err, "this should have failed")
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

			rc, err := f.open(context.Background(), req, "", 0)
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
	tests := []struct {
		name                   string
		mockedStatus           int
		mockedErr              error
		want                   io.ReadCloser
		wantErr                bool
		wantServerMessageError bool
		wantPlainError         bool
		retries                int
	}{
		{
			name:         "should return status and error.message if it exists if the registry request fails",
			mockedStatus: 500,
			mockedErr: Errors{Error{
				Code:    ErrorCodeUnknown,
				Message: "Test Error",
			}},
			want:                   nil,
			wantErr:                true,
			wantServerMessageError: true,
		},
		{
			name:           "should return just status if the registry request fails and does not return a docker error",
			mockedStatus:   500,
			mockedErr:      errors.New("Non-docker error"),
			want:           nil,
			wantErr:        true,
			wantPlainError: true,
		}, {
			name:           "should return StatusRequestTimeout after 5 retries",
			mockedStatus:   http.StatusRequestTimeout,
			mockedErr:      errors.New(http.StatusText(http.StatusRequestTimeout)),
			want:           nil,
			wantErr:        true,
			wantPlainError: true,
			retries:        5,
		}, {
			name:           "should return StatusTooManyRequests after 5 retries",
			mockedStatus:   http.StatusTooManyRequests,
			mockedErr:      errors.New(http.StatusText(http.StatusTooManyRequests)),
			want:           nil,
			wantErr:        true,
			wantPlainError: true,
			retries:        5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if tt.retries > 0 {
					tt.retries--
				}
				rw.WriteHeader(tt.mockedStatus)
				bytes, _ := json.Marshal(tt.mockedErr)
				rw.Write(bytes)
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

			req := f.request(host, http.MethodGet)

			got, err := f.open(context.TODO(), req, "", 0)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.retries, 0)
			if tt.wantErr {
				var expectedError error
				if tt.wantServerMessageError {
					expectedError = fmt.Errorf("unexpected status code %v/ns: %v %s - Server message: %s", s.URL, tt.mockedStatus, http.StatusText(tt.mockedStatus), tt.mockedErr.Error())
				} else if tt.wantPlainError {
					expectedError = fmt.Errorf("unexpected status code %v/ns: %v %s", s.URL, tt.mockedStatus, http.StatusText(tt.mockedStatus))
				}
				assert.Equal(t, expectedError.Error(), err.Error())

			}

		})
	}
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
