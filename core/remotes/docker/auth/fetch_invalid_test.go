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

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchTokenWithOAuth_InvalidJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>nope</body></html>"))
	}))
	defer ts.Close()

	to := TokenOptions{
		Realm:    ts.URL,
		Service:  "svc",
		Username: "u",
		Secret:   "s",
	}

	_, err := FetchTokenWithOAuth(context.Background(), http.DefaultClient, nil, "cid", to)
	if err == nil {
		t.Fatalf("expected error")
	}

	var inv *ErrInvalidTokenResponse
	if !errors.As(err, &inv) {
		t.Fatalf("expected ErrInvalidTokenResponse, got %T: %v", err, err)
	}
	if len(inv.Body) == 0 {
		t.Fatalf("expected body to be captured")
	}
	if inv.ContentType == "" {
		t.Fatalf("expected content-type to be captured")
	}
}
