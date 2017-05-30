/*
Copyright 2017 The Kubernetes Authors.

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

package agents

import "io"

// StreamType is the type of the stream, stdout/stderr.
type StreamType string

const (
	// Stdout stream type.
	Stdout StreamType = "stdout"
	// Stderr stream type.
	Stderr StreamType = "stderr"
)

// Agent is the a running agent perform a specific task, e.g. redirect and
// decorate log, redirect stream etc.
type Agent interface {
	// Start starts the logger.
	Start() error
}

// AgentFactory is the factory to create required agents.
type AgentFactory interface {
	// NewSandboxLogger creates a sandbox logging agent.
	NewSandboxLogger(io.ReadCloser) Agent
	// NewContainerLogger creates a container logging agent.
	NewContainerLogger(string, StreamType, io.ReadCloser) Agent
}

type agentFactory struct{}

// NewAgentFactory creates a new agent factory.
func NewAgentFactory() AgentFactory {
	return &agentFactory{}
}
