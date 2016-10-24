package supervisor

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/containerd/runtime"
)

func TestEventLogCompat(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Errorf("Failed to create temp dir: %v", err)
	}

	path := filepath.Join(tmpDir, "events.log")
	eventf, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|os.O_TRUNC, 0755)
	if err != nil {
		t.Errorf("Failed to create event logs: %v", err)
	}

	s := &Supervisor{stateDir: tmpDir}

	enc := json.NewEncoder(eventf)
	for _, ev := range []eventV1{
		{
			Event: Event{
				ID:        "abc",
				Type:      "event",
				Timestamp: time.Now(),
				PID:       "42",
			},
			Status: -1,
		},
		{
			Event: Event{
				ID:        "abc",
				Type:      "event",
				Timestamp: time.Now(),
				PID:       "42",
			},
			Status: 42,
		},
	} {
		enc.Encode(ev)
	}
	eventf.Close()

	err = readEventLog(s)
	if err != nil {
		t.Errorf("Failed to read event logs: %v", err)
	}

	if s.eventLog[0].Status != runtime.UnknownStatus {
		t.Errorf("Improper event status: %v", s.eventLog[0].Status)
	}

	if s.eventLog[1].Status != 42 {
		t.Errorf("Improper event status: %v", s.eventLog[1].Status)
	}
}
