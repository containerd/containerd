package http

import (
	"net/http"
	"strings"
)

type prefixHandler struct {
	prefix string
	next   http.Handler
}

func newPrefixHandler(prefix string, next http.Handler) http.Handler {
	return prefixHandler{prefix, next}
}

func (h prefixHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, h.prefix) {
		http.Error(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}

	r.URL.Path = strings.TrimPrefix(r.URL.Path, h.prefix)
	h.next.ServeHTTP(w, r)
}
