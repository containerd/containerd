// +build windows
// +build amd64 arm64 386

package etw

import (
	"bytes"
	"encoding/binary"
	"unsafe"

	"github.com/Microsoft/go-winio/pkg/guid"
	"golang.org/x/sys/windows"
)

type providerOpts struct {
	callback EnableCallback
	id       guid.GUID
	group    guid.GUID
}

// ProviderOpt allows the caller to specify provider options to
// NewProviderWithOptions
type ProviderOpt func(*providerOpts)

// WithCallback is used to provide a callback option to NewProviderWithOptions
func WithCallback(callback EnableCallback) ProviderOpt {
	return func(opts *providerOpts) {
		opts.callback = callback
	}
}

// WithID is used to provide a provider ID option to NewProviderWithOptions
func WithID(id guid.GUID) ProviderOpt {
	return func(opts *providerOpts) {
		opts.id = id
	}
}

// WithGroup is used to provide a provider group option to
// NewProviderWithOptions
func WithGroup(group guid.GUID) ProviderOpt {
	return func(opts *providerOpts) {
		opts.group = group
	}
}

// NewProviderWithID creates and registers a new ETW provider, allowing the
// provider ID to be manually specified. This is most useful when there is an
// existing provider ID that must be used to conform to existing diagnostic
// infrastructure.
func NewProviderWithID(name string, id guid.GUID, callback EnableCallback) (provider *Provider, err error) {
	return NewProviderWithOptions(name, WithID(id), WithCallback(callback))
}

// NewProviderWithOptions creates and registers a new ETW provider, allowing
// the provider ID and Group to be manually specified. This is most useful when
// there is an existing provider ID that must be used to conform to existing
// diagnostic infrastructure.
func NewProviderWithOptions(name string, options ...ProviderOpt) (provider *Provider, err error) {
	var opts providerOpts
	for _, opt := range options {
		opt(&opts)
	}

	if opts.id == (guid.GUID{}) {
		opts.id = providerIDFromName(name)
	}

	providerCallbackOnce.Do(func() {
		globalProviderCallback = windows.NewCallback(providerCallbackAdapter)
	})

	provider = providers.newProvider()
	defer func(provider *Provider) {
		if err != nil {
			providers.removeProvider(provider)
		}
	}(provider)
	provider.ID = opts.id
	provider.callback = opts.callback

	if err := eventRegister((*windows.GUID)(&provider.ID), globalProviderCallback, uintptr(provider.index), &provider.handle); err != nil {
		return nil, err
	}

	trait := &bytes.Buffer{}
	if opts.group != (guid.GUID{}) {
		binary.Write(trait, binary.LittleEndian, uint16(0)) // Write empty size for buffer (update later)
		binary.Write(trait, binary.LittleEndian, uint8(1))  // EtwProviderTraitTypeGroup
		traitArray := opts.group.ToWindowsArray()           // Append group guid
		trait.Write(traitArray[:])
		binary.LittleEndian.PutUint16(trait.Bytes(), uint16(trait.Len())) // Update size
	}

	metadata := &bytes.Buffer{}
	binary.Write(metadata, binary.LittleEndian, uint16(0)) // Write empty size for buffer (to update later)
	metadata.WriteString(name)
	metadata.WriteByte(0)                                                   // Null terminator for name
	trait.WriteTo(metadata)                                                 // Add traits if applicable
	binary.LittleEndian.PutUint16(metadata.Bytes(), uint16(metadata.Len())) // Update the size at the beginning of the buffer
	provider.metadata = metadata.Bytes()

	if err := eventSetInformation(
		provider.handle,
		eventInfoClassProviderSetTraits,
		uintptr(unsafe.Pointer(&provider.metadata[0])),
		uint32(len(provider.metadata))); err != nil {

		return nil, err
	}

	return provider, nil
}
