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

package net

import (
	"context"

	"github.com/containernetworking/cni/libcni"
	types100 "github.com/containernetworking/cni/pkg/types/100"
)

// API defines the top level interfaces exposed by the Networks Plugin
// Clients will these interfaces to create or locate Network Managers
type API interface {
	// NetManager creates a Network Manager identified by the given name
	NewManager(name string, opts ...ManagerOpt) (Manager, error)

	// Manager returns a Network Manager identified by the given name
	Manager(name string) Manager
}

// Manager defines the interfaces of a Network Manager. A network manager
// manages networks and attachments.
type Manager interface {
	// Name returns the name of the network manager
	Name() string

	// Create a network definition
	Create(ctx context.Context, opts ...NetworkOpt) (Network, error)

	// Returns a network identified by the name
	Network(ctx context.Context, name string) (Network, error)

	// List all network definition managed by this manager instance
	List(ctx context.Context) []Network

	// Attachment returns a network attachment identified by id
	Attachment(ctx context.Context, id string) (Attachment, error)
}

// Network defines the interfaces of a Network definition.
// A network is a CNI concept that is modeled by a conf/conflist json file
type Network interface {
	// Name returns the name of the network
	Name() string

	// Manager returns the network manager that manages this network
	Manager() string

	// Config returns the CNI NetworkConfigList that describes the network
	Config() *libcni.NetworkConfigList

	// Update the network configuration
	Update(ctx context.Context, opts ...NetworkOpt) error

	// Delete the network
	Delete(ctx context.Context) error

	// Labels returns the labels associated with the network
	Labels() map[string]string

	// Attach the network to a container/sandbox
	Attach(ctx context.Context, opts ...AttachmentOpt) (Attachment, error)

	// List all attachments for this network
	List(ctx context.Context) []Attachment
}

// Attachment is a CNI concept that allows a container to “join” a network
type Attachment interface {
	// ID returns the id of the attachment
	ID() string

	// Manager returns the network manager that owns the attachment
	Manager() string

	// Network returns the name of the network that this attachment is created for
	Network() string

	// Container returns the id of the container/sandbox for this attachment
	Container() string

	// IFName returns the interface name inside the NS namespace for this attachment
	IFName() string

	// NSPath returns the network namespace for this attachment
	NSPath() string

	// Result returns the CNI attachment result
	Result() *types100.Result

	// GCOwnerLables returns the labels that should be applied to the owner object
	// such as container/sandbox to establish ownership models for garbage collection
	GCOwnerLables() map[string]string

	// Remove the attachment
	Remove(ctx context.Context) error

	// Check whether the current status of the attachment is as expected
	Check(ctx context.Context) error
}
