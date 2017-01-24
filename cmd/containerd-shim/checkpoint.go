package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type checkpoint struct {
	// Timestamp is the time that checkpoint happened
	Created time.Time `json:"created"`
	// Name is the name of the checkpoint
	Name string `json:"name"`
	// TCP checkpoints open tcp connections
	TCP bool `json:"tcp"`
	// UnixSockets persists unix sockets in the checkpoint
	UnixSockets bool `json:"unixSockets"`
	// Shell persists tty sessions in the checkpoint
	Shell bool `json:"shell"`
	// Exit exits the container after the checkpoint is finished
	Exit bool `json:"exit"`
	// EmptyNS tells CRIU not to restore a particular namespace
	EmptyNS []string `json:"emptyNS,omitempty"`
}

func loadCheckpoint(checkpointPath string) (*checkpoint, error) {
	f, err := os.Open(filepath.Join(checkpointPath, "config.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cpt checkpoint
	if err := json.NewDecoder(f).Decode(&cpt); err != nil {
		return nil, err
	}
	return &cpt, nil
}
