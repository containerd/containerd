package pprof

import (
	// expvar init routine adds the "/debug/vars" handler
	_ "expvar"
	"net/http"
	// net/http/pprof installs the "/debug/pprof/{block,heap,goroutine,threadcreate}" handler
	_ "net/http/pprof"

	"github.com/Sirupsen/logrus"
)

// Enable registers the "/debug/pprof" handler
func Enable(address string) {
	http.Handle("/", http.RedirectHandler("/debug/pprof", http.StatusMovedPermanently))

	go http.ListenAndServe(address, nil)
	logrus.Debug("pprof listening in address %s", address)
}
