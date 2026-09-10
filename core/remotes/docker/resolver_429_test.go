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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	remoteerrors "github.com/containerd/containerd/v2/core/remotes/errors"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertRange(t *testing.T, actual, min, max time.Duration) {
	t.Helper()
	assert.GreaterOrEqual(t, actual, min)
	assert.LessOrEqual(t, actual, max)
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name         string
		header       string
		wantOk       bool
		wantMin      time.Duration
		wantMax      time.Duration
		wantDuration time.Duration
	}{
		{
			name:   "empty header",
			header: "",
			wantOk: false,
		},
		{
			name:   "whitespace only",
			header: "   ",
			wantOk: false,
		},
		{
			name:         "valid delta seconds",
			header:       "120",
			wantOk:       true,
			wantDuration: 120 * time.Second,
		},
		{
			name:         "valid delta seconds with spaces",
			header:       "  5  ",
			wantOk:       true,
			wantDuration: 5 * time.Second,
		},
		{
			name:         "zero delta seconds",
			header:       "0",
			wantOk:       true,
			wantDuration: 0,
		},
		{
			name:   "negative delta seconds invalid",
			header: "-10",
			wantOk: false,
		},
		{
			name:   "delta seconds overflow time.Duration",
			header: "9223372037",
			wantOk: false,
		},
		{
			name:   "delta seconds overflow int64",
			header: "99999999999999999999999999999",
			wantOk: false,
		},
		{
			name:         "HTTP date in the past",
			header:       "Wed, 21 Oct 2015 07:28:00 GMT",
			wantOk:       true,
			wantDuration: 0,
		},
		{
			name:    "HTTP date in the future",
			header:  time.Now().Add(60 * time.Second).UTC().Format(http.TimeFormat),
			wantOk:  true,
			wantMin: 55 * time.Second,
			wantMax: 65 * time.Second,
		},
		{
			name:   "invalid header string",
			header: "not-a-number-or-date",
			wantOk: false,
		},
		{
			name:   "invalid suffix like 120s",
			header: "120s",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := parseRetryAfter(tt.header)
			assert.Equal(t, tt.wantOk, ok)
			if !tt.wantOk {
				return
			}
			if tt.wantMin > 0 || tt.wantMax > 0 {
				assertRange(t, d, tt.wantMin, tt.wantMax)
			} else {
				assert.Equal(t, tt.wantDuration, d)
			}
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	base := 500 * time.Millisecond
	max := 5 * time.Second

	// Attempt 1: 500ms base - up to 20% jitter (100ms) -> [400ms, 500ms]
	for range 10 {
		d := calculateBackoff(1, base, max)
		assertRange(t, d, 400*time.Millisecond, 500*time.Millisecond)
	}

	// Attempt 2: 1000ms base - up to 20% jitter (200ms) -> [800ms, 1000ms]
	for range 10 {
		d := calculateBackoff(2, base, max)
		assertRange(t, d, 800*time.Millisecond, 1000*time.Millisecond)
	}

	// Attempt 3: 2000ms base - up to 20% jitter (400ms) -> [1600ms, 2000ms]
	for range 10 {
		d := calculateBackoff(3, base, max)
		assertRange(t, d, 1600*time.Millisecond, 2000*time.Millisecond)
	}

	// Attempt 4: 4000ms base - up to 20% jitter (800ms) -> [3200ms, 4000ms]
	for range 10 {
		d := calculateBackoff(4, base, max)
		assertRange(t, d, 3200*time.Millisecond, 4000*time.Millisecond)
	}

	// Attempt 5+: capped at max (5s) - up to 20% jitter (1000ms) -> [4000ms, 5000ms]
	for range 10 {
		d := calculateBackoff(5, base, max)
		assertRange(t, d, 4000*time.Millisecond, 5000*time.Millisecond)
	}

	// Attempt 100: no integer overflow, still capped at max with jitter
	d := calculateBackoff(100, base, max)
	assertRange(t, d, 4000*time.Millisecond, 5000*time.Millisecond)

	// Attempt 0 or negative: treated as attempt 1
	d0 := calculateBackoff(0, base, max)
	assertRange(t, d0, 400*time.Millisecond, 500*time.Millisecond)
}

func TestRetry429WithRetryAfterHeader(t *testing.T) {
	t.Run("eventual success", func(t *testing.T) {
		var (
			sleepMu        sync.Mutex
			sleptDurations []time.Duration
			attempts       atomic.Int32
		)
		sleeper := func(ctx context.Context, d time.Duration) error {
			sleepMu.Lock()
			sleptDurations = append(sleptDurations, d)
			sleepMu.Unlock()
			return nil
		}

		s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			current := attempts.Add(1)
			if current <= 2 {
				rw.Header().Set("Retry-After", strconv.Itoa(int(current)))
				rw.WriteHeader(http.StatusTooManyRequests)
				return
			}

			rw.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(s.Close)

		u, err := url.Parse(s.URL)
		require.NoError(t, err)

		refspec, err := reference.Parse(u.Host + "/test/image:latest")
		require.NoError(t, err)

		req := &request{
			method:  http.MethodGet,
			path:    "/v2/test/image/manifests/latest",
			host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
			refspec: refspec,
			sleeper: sleeper,
		}

		resp, err := req.doWithRetries(context.Background(), true, withErrorCheck)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(3), attempts.Load())
		sleepMu.Lock()
		defer sleepMu.Unlock()
		require.Len(t, sleptDurations, 2)
		assert.Equal(t, 1*time.Second, sleptDurations[0])
		assert.Equal(t, 2*time.Second, sleptDurations[1])
	})

	t.Run("retries exhausted", func(t *testing.T) {
		var (
			sleepMu        sync.Mutex
			sleptDurations []time.Duration
			attempts       atomic.Int32
		)
		sleeper := func(ctx context.Context, d time.Duration) error {
			sleepMu.Lock()
			sleptDurations = append(sleptDurations, d)
			sleepMu.Unlock()
			return nil
		}

		s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			rw.Header().Set("Retry-After", "30")
			rw.WriteHeader(http.StatusTooManyRequests)
			rw.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"pull rate limit reached"}]}`))
		}))
		t.Cleanup(s.Close)

		u, err := url.Parse(s.URL)
		require.NoError(t, err)

		refspec, err := reference.Parse(u.Host + "/test/jsonerr:latest")
		require.NoError(t, err)

		req := &request{
			method:  http.MethodGet,
			path:    "/v2/test/jsonerr/manifests/latest",
			host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
			refspec: refspec,
			sleeper: sleeper,
		}

		_, err = req.doWithRetries(context.Background(), true, withErrorCheck)
		require.Error(t, err)

		assert.Equal(t, int32(5), attempts.Load())
		sleepMu.Lock()
		defer sleepMu.Unlock()
		require.Len(t, sleptDurations, 4)

		var statusErr remoteerrors.ErrUnexpectedStatus
		require.True(t, errors.As(err, &statusErr))
		assert.Equal(t, http.StatusTooManyRequests, statusErr.StatusCode)
		assert.Equal(t, "30", statusErr.RetryAfter)

		errStr := err.Error()
		assert.Contains(t, errStr, "(Retry-After: 30)")
		assert.Contains(t, errStr, "toomanyrequests")
		assert.Contains(t, errStr, "pull rate limit reached")
	})
}

func TestRetry429ExponentialBackoff(t *testing.T) {
	var (
		sleepMu        sync.Mutex
		sleptDurations []time.Duration
		attempts       atomic.Int32
	)
	sleeper := func(ctx context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleptDurations = append(sleptDurations, d)
		sleepMu.Unlock()
		return nil
	}

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		current := attempts.Add(1)
		if current <= 2 {
			rw.WriteHeader(http.StatusTooManyRequests)
			return
		}

		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)

	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	refspec, err := reference.Parse(u.Host + "/test/backoff:latest")
	require.NoError(t, err)

	req := &request{
		method:  http.MethodGet,
		path:    "/v2/test/backoff/manifests/latest",
		host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
		refspec: refspec,
		sleeper: sleeper,
	}

	resp, err := req.doWithRetries(context.Background(), true, withErrorCheck)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(3), attempts.Load())
	sleepMu.Lock()
	defer sleepMu.Unlock()
	require.Len(t, sleptDurations, 2)

	// Attempt 1 backoff: [400ms, 500ms]
	assertRange(t, sleptDurations[0], 400*time.Millisecond, 500*time.Millisecond)

	// Attempt 2 backoff: [800ms, 1000ms]
	assertRange(t, sleptDurations[1], 800*time.Millisecond, 1000*time.Millisecond)
}

func TestRetry429NotLastHostFailsImmediately(t *testing.T) {
	var attempts atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		rw.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(s.Close)

	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	refspec, err := reference.Parse(u.Host + "/test/not-lasthost:latest")
	require.NoError(t, err)

	req := &request{
		method:  http.MethodGet,
		path:    "/v2/test/not-lasthost/manifests/latest",
		host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
		refspec: refspec,
	}

	// With lastHost=false, 429 should not retry, allowing fallback to next host
	_, err = req.doWithRetries(context.Background(), false, withErrorCheck)
	require.Error(t, err)
	assert.Equal(t, int32(1), attempts.Load())

	var statusErr remoteerrors.ErrUnexpectedStatus
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, http.StatusTooManyRequests, statusErr.StatusCode)
}

func TestRetry429RespectsRetryAfterWithoutMaxDelayCap(t *testing.T) {
	var (
		sleepMu        sync.Mutex
		sleptDurations []time.Duration
		attempts       atomic.Int32
	)
	sleeper := func(ctx context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleptDurations = append(sleptDurations, d)
		sleepMu.Unlock()
		return nil
	}

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		current := attempts.Add(1)
		if current <= 1 {
			rw.Header().Set("Retry-After", "60")
			rw.WriteHeader(http.StatusTooManyRequests)
			return
		}

		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)

	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	refspec, err := reference.Parse(u.Host + "/test/no-cap:latest")
	require.NoError(t, err)

	req := &request{
		method:  http.MethodGet,
		path:    "/v2/test/no-cap/manifests/latest",
		host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
		refspec: refspec,
		sleeper: sleeper,
	}

	resp, err := req.doWithRetries(context.Background(), true, withErrorCheck)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	// Retry-After (60s) is respected
	assert.Equal(t, int32(2), attempts.Load())
	sleepMu.Lock()
	defer sleepMu.Unlock()
	require.Len(t, sleptDurations, 1)
	assert.Equal(t, 60*time.Second, sleptDurations[0])
}

func TestRetry429ExceedsMaxRetryAfterFailsImmediately(t *testing.T) {
	var (
		sleepMu        sync.Mutex
		sleptDurations []time.Duration
		attempts       atomic.Int32
	)
	sleeper := func(ctx context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleptDurations = append(sleptDurations, d)
		sleepMu.Unlock()
		return nil
	}

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		rw.Header().Set("Retry-After", "61")
		rw.WriteHeader(http.StatusTooManyRequests)
		rw.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"rate limit exceeded, try later"}]}`))
	}))
	t.Cleanup(s.Close)

	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	refspec, err := reference.Parse(u.Host + "/test/excessive:latest")
	require.NoError(t, err)

	req := &request{
		method:  http.MethodGet,
		path:    "/v2/test/excessive/manifests/latest",
		host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
		refspec: refspec,
		sleeper: sleeper,
	}

	_, err = req.doWithRetries(context.Background(), true, withErrorCheck)
	require.Error(t, err)

	// Fails immediately on first attempt without retrying or sleeping
	assert.Equal(t, int32(1), attempts.Load())
	sleepMu.Lock()
	defer sleepMu.Unlock()
	assert.Empty(t, sleptDurations)

	var statusErr remoteerrors.ErrUnexpectedStatus
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, http.StatusTooManyRequests, statusErr.StatusCode)
	assert.Equal(t, "61", statusErr.RetryAfter)
	assert.Contains(t, err.Error(), "(Retry-After: 61)")
	assert.Contains(t, err.Error(), "rate limit exceeded, try later")
}

func TestRetry429ContextCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Retry-After", "10")
		rw.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(s.Close)

	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	refspec, err := reference.Parse(u.Host + "/test/cancel:latest")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	start := time.Now()
	req := &request{
		method:  http.MethodGet,
		path:    "/v2/test/cancel/manifests/latest",
		host:    RegistryHost{Client: s.Client(), Host: u.Host, Scheme: u.Scheme},
		refspec: refspec,
		sleeper: func(ctx context.Context, d time.Duration) error {
			// Trigger context cancel while sleeping
			cancel()
			return defaultSleep(ctx, d)
		},
	}

	_, err = req.doWithRetries(ctx, true, withErrorCheck)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRetry429Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		// Verify defaultSleep advances virtual time durably and immediately
		start := time.Now()
		err := defaultSleep(ctx, 3*time.Second)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 3*time.Second)

		// Verify context timeout terminates defaultSleep with DeadlineExceeded in virtual time
		timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		defer cancel()

		start = time.Now()
		err = defaultSleep(timeoutCtx, 5*time.Second)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		elapsed := time.Since(start)
		assert.GreaterOrEqual(t, elapsed, 1*time.Second)
		assert.Less(t, elapsed, 5*time.Second)
	})
}
