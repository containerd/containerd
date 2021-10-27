package cmds

import (
	"strings"
	"sync"
	"time"
)

// ReqLogEntry represent a log entry for a request.
type ReqLogEntry struct {
	StartTime time.Time
	EndTime   time.Time
	Active    bool
	Command   string
	Options   map[string]interface{}
	Args      []string
	ID        int
}

// Copy copies a log entry and returns a pointer to the copy.
func (r *ReqLogEntry) Copy() *ReqLogEntry {
	out := *r
	return &out
}

// ReqLog represents a request log.
type ReqLog struct {
	Requests []*ReqLogEntry
	nextID   int
	lock     sync.Mutex
	keep     time.Duration
}

// Add ads an entry to the log for the given request.
func (rl *ReqLog) Add(req *Request) *ReqLogEntry {
	rle := &ReqLogEntry{
		StartTime: time.Now(),
		Active:    true,
		Command:   strings.Join(req.Path, "/"),
		Options:   req.Options,
		Args:      req.Arguments,
		ID:        rl.nextID,
	}

	rl.AddEntry(rle)
	return rle
}

// AddEntry adds an entry to the log.
func (rl *ReqLog) AddEntry(rle *ReqLogEntry) {
	rl.lock.Lock()
	defer rl.lock.Unlock()

	rl.nextID++
	rl.Requests = append(rl.Requests, rle)

	if rle == nil || !rle.Active {
		rl.maybeCleanup()
	}
}

// ClearInactive clears any inactive requests from the log.
func (rl *ReqLog) ClearInactive() {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	k := rl.keep
	rl.keep = 0
	rl.cleanup()
	rl.keep = k
}

func (rl *ReqLog) maybeCleanup() {
	// only do it every so often or it might
	// become a perf issue
	if len(rl.Requests)%10 == 0 {
		rl.cleanup()
	}
}

func (rl *ReqLog) cleanup() {
	i := 0
	now := time.Now()
	for j := 0; j < len(rl.Requests); j++ {
		rj := rl.Requests[j]
		if rj.Active || rl.Requests[j].EndTime.Add(rl.keep).After(now) {
			rl.Requests[i] = rl.Requests[j]
			i++
		}
	}
	rl.Requests = rl.Requests[:i]
}

func (rl *ReqLog) SetKeepTime(t time.Duration) {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	rl.keep = t
}

// Report generates a copy of all the entries in the requestlog
func (rl *ReqLog) Report() []*ReqLogEntry {
	rl.lock.Lock()
	defer rl.lock.Unlock()
	out := make([]*ReqLogEntry, len(rl.Requests))

	for i, e := range rl.Requests {
		out[i] = e.Copy()
	}

	return out
}

func (rl *ReqLog) Finish(rle *ReqLogEntry) {
	rl.lock.Lock()
	defer rl.lock.Unlock()

	rle.Active = false
	rle.EndTime = time.Now()

	rl.maybeCleanup()
}
