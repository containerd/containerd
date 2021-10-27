package options

import "fmt"

// PinAddSettings represent the settings for PinAPI.Add
type PinAddSettings struct {
	Recursive bool
}

// PinLsSettings represent the settings for PinAPI.Ls
type PinLsSettings struct {
	Type string
}

// PinIsPinnedSettings represent the settings for PinAPI.IsPinned
type PinIsPinnedSettings struct {
	WithType string
}

// PinRmSettings represents the settings for PinAPI.Rm
type PinRmSettings struct {
	Recursive bool
}

// PinUpdateSettings represent the settings for PinAPI.Update
type PinUpdateSettings struct {
	Unpin bool
}

// PinAddOption is the signature of an option for PinAPI.Add
type PinAddOption func(*PinAddSettings) error

// PinLsOption is the signature of an option for PinAPI.Ls
type PinLsOption func(*PinLsSettings) error

// PinIsPinnedOption is the signature of an option for PinAPI.IsPinned
type PinIsPinnedOption func(*PinIsPinnedSettings) error

// PinRmOption is the signature of an option for PinAPI.Rm
type PinRmOption func(*PinRmSettings) error

// PinUpdateOption is the signature of an option for PinAPI.Update
type PinUpdateOption func(*PinUpdateSettings) error

// PinAddOptions compile a series of PinAddOption into a ready to use
// PinAddSettings and set the default values.
func PinAddOptions(opts ...PinAddOption) (*PinAddSettings, error) {
	options := &PinAddSettings{
		Recursive: true,
	}

	for _, opt := range opts {
		err := opt(options)
		if err != nil {
			return nil, err
		}
	}

	return options, nil
}

// PinLsOptions compile a series of PinLsOption into a ready to use
// PinLsSettings and set the default values.
func PinLsOptions(opts ...PinLsOption) (*PinLsSettings, error) {
	options := &PinLsSettings{
		Type: "all",
	}

	for _, opt := range opts {
		err := opt(options)
		if err != nil {
			return nil, err
		}
	}

	return options, nil
}

// PinIsPinnedOptions compile a series of PinIsPinnedOption into a ready to use
// PinIsPinnedSettings and set the default values.
func PinIsPinnedOptions(opts ...PinIsPinnedOption) (*PinIsPinnedSettings, error) {
	options := &PinIsPinnedSettings{
		WithType: "all",
	}

	for _, opt := range opts {
		err := opt(options)
		if err != nil {
			return nil, err
		}
	}

	return options, nil
}

// PinRmOptions compile a series of PinRmOption into a ready to use
// PinRmSettings and set the default values.
func PinRmOptions(opts ...PinRmOption) (*PinRmSettings, error) {
	options := &PinRmSettings{
		Recursive: true,
	}

	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, err
		}
	}

	return options, nil
}

// PinUpdateOptions compile a series of PinUpdateOption into a ready to use
// PinUpdateSettings and set the default values.
func PinUpdateOptions(opts ...PinUpdateOption) (*PinUpdateSettings, error) {
	options := &PinUpdateSettings{
		Unpin: true,
	}

	for _, opt := range opts {
		err := opt(options)
		if err != nil {
			return nil, err
		}
	}

	return options, nil
}

type pinOpts struct {
	Ls       pinLsOpts
	IsPinned pinIsPinnedOpts
}

// Pin provide an access to all the options for the Pin API.
var Pin pinOpts

type pinLsOpts struct{}

// All is an option for Pin.Ls which will make it return all pins. It is
// the default
func (pinLsOpts) All() PinLsOption {
	return Pin.Ls.pinType("all")
}

// Recursive is an option for Pin.Ls which will make it only return recursive
// pins
func (pinLsOpts) Recursive() PinLsOption {
	return Pin.Ls.pinType("recursive")
}

// Direct is an option for Pin.Ls which will make it only return direct (non
// recursive) pins
func (pinLsOpts) Direct() PinLsOption {
	return Pin.Ls.pinType("direct")
}

// Indirect is an option for Pin.Ls which will make it only return indirect pins
// (objects referenced by other recursively pinned objects)
func (pinLsOpts) Indirect() PinLsOption {
	return Pin.Ls.pinType("indirect")
}

// Type is an option for Pin.Ls which will make it only return pins of the given
// type.
//
// Supported values:
// * "direct" - directly pinned objects
// * "recursive" - roots of recursive pins
// * "indirect" - indirectly pinned objects (referenced by recursively pinned
//    objects)
// * "all" - all pinned objects (default)
func (pinLsOpts) Type(typeStr string) (PinLsOption, error) {
	switch typeStr {
	case "all", "direct", "indirect", "recursive":
		return Pin.Ls.pinType(typeStr), nil
	default:
		return nil, fmt.Errorf("invalid type '%s', must be one of {direct, indirect, recursive, all}", typeStr)
	}
}

// pinType is an option for Pin.Ls which allows to specify which pin types should
// be returned
//
// Supported values:
// * "direct" - directly pinned objects
// * "recursive" - roots of recursive pins
// * "indirect" - indirectly pinned objects (referenced by recursively pinned
//    objects)
// * "all" - all pinned objects (default)
func (pinLsOpts) pinType(t string) PinLsOption {
	return func(settings *PinLsSettings) error {
		settings.Type = t
		return nil
	}
}

type pinIsPinnedOpts struct{}

// All is an option for Pin.IsPinned which will make it search in all type of pins.
// It is the default
func (pinIsPinnedOpts) All() PinIsPinnedOption {
	return Pin.IsPinned.pinType("all")
}

// Recursive is an option for Pin.IsPinned which will make it only search in
// recursive pins
func (pinIsPinnedOpts) Recursive() PinIsPinnedOption {
	return Pin.IsPinned.pinType("recursive")
}

// Direct is an option for Pin.IsPinned which will make it only search in direct
// (non recursive) pins
func (pinIsPinnedOpts) Direct() PinIsPinnedOption {
	return Pin.IsPinned.pinType("direct")
}

// Indirect is an option for Pin.IsPinned which will make it only search indirect
// pins (objects referenced by other recursively pinned objects)
func (pinIsPinnedOpts) Indirect() PinIsPinnedOption {
	return Pin.IsPinned.pinType("indirect")
}

// Type is an option for Pin.IsPinned which will make it only search pins of the given
// type.
//
// Supported values:
// * "direct" - directly pinned objects
// * "recursive" - roots of recursive pins
// * "indirect" - indirectly pinned objects (referenced by recursively pinned
//    objects)
// * "all" - all pinned objects (default)
func (pinIsPinnedOpts) Type(typeStr string) (PinIsPinnedOption, error) {
	switch typeStr {
	case "all", "direct", "indirect", "recursive":
		return Pin.IsPinned.pinType(typeStr), nil
	default:
		return nil, fmt.Errorf("invalid type '%s', must be one of {direct, indirect, recursive, all}", typeStr)
	}
}

// pinType is an option for Pin.IsPinned which allows to specify which pin type the given
// pin is expected to be, speeding up the research.
//
// Supported values:
// * "direct" - directly pinned objects
// * "recursive" - roots of recursive pins
// * "indirect" - indirectly pinned objects (referenced by recursively pinned
//    objects)
// * "all" - all pinned objects (default)
func (pinIsPinnedOpts) pinType(t string) PinIsPinnedOption {
	return func(settings *PinIsPinnedSettings) error {
		settings.WithType = t
		return nil
	}
}

// Recursive is an option for Pin.Add which specifies whether to pin an entire
// object tree or just one object. Default: true
func (pinOpts) Recursive(recursive bool) PinAddOption {
	return func(settings *PinAddSettings) error {
		settings.Recursive = recursive
		return nil
	}
}

// RmRecursive is an option for Pin.Rm which specifies whether to recursively
// unpin the object linked to by the specified object(s). This does not remove
// indirect pins referenced by other recursive pins.
func (pinOpts) RmRecursive(recursive bool) PinRmOption {
	return func(settings *PinRmSettings) error {
		settings.Recursive = recursive
		return nil
	}
}

// Unpin is an option for Pin.Update which specifies whether to remove the old pin.
// Default is true.
func (pinOpts) Unpin(unpin bool) PinUpdateOption {
	return func(settings *PinUpdateSettings) error {
		settings.Unpin = unpin
		return nil
	}
}
