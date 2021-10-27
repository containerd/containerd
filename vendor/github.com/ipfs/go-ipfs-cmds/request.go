package cmds

import (
	"context"
	"fmt"
	"reflect"

	files "github.com/ipfs/go-ipfs-files"
)

// Request represents a call to a command from a consumer
type Request struct {
	Context       context.Context
	Root, Command *Command

	Path      []string
	Arguments []string
	Options   OptMap

	Files files.Directory

	bodyArgs *arguments
}

// NewRequest returns a request initialized with given arguments
// An non-nil error will be returned if the provided option values are invalid
func NewRequest(ctx context.Context,
	path []string,
	opts OptMap,
	args []string,
	file files.Directory,
	root *Command,
) (*Request, error) {

	cmd, err := root.Get(path)
	if err != nil {
		return nil, err
	}

	options, err := checkAndConvertOptions(root, opts, path)

	req := &Request{
		Path:      path,
		Options:   options,
		Arguments: args,
		Files:     file,
		Root:      root,
		Command:   cmd,
		Context:   ctx,
	}

	return req, err
}

// BodyArgs returns a scanner that returns arguments passed in the body as tokens.
//
// Returns nil if there are no arguments to be consumed via stdin.
func (req *Request) BodyArgs() StdinArguments {
	// dance to make sure we return an *untyped* nil.
	// DO NOT just return `req.bodyArgs`.
	// If you'd like to complain, go to
	// https://github.com/golang/go/issues/.
	if req.bodyArgs != nil {
		return req.bodyArgs
	}
	return nil
}

// ParseBodyArgs parses arguments in the request body.
func (req *Request) ParseBodyArgs() error {
	s := req.BodyArgs()
	if s == nil {
		return nil
	}

	for s.Scan() {
		req.Arguments = append(req.Arguments, s.Argument())
	}
	return s.Err()
}

// SetOption sets a request option.
func (req *Request) SetOption(name string, value interface{}) {
	optDefs, err := req.Root.GetOptions(req.Path)
	optDef, found := optDefs[name]

	if req.Options == nil {
		req.Options = map[string]interface{}{}
	}

	// unknown option, simply set the value and return
	// TODO we might error out here instead
	if err != nil || !found {
		req.Options[name] = value
		return
	}

	name = optDef.Name()
	req.Options[name] = value
}

func checkAndConvertOptions(root *Command, opts OptMap, path []string) (OptMap, error) {
	optDefs, err := root.GetOptions(path)
	options := make(OptMap)

	if err != nil {
		return options, err
	}
	for k, v := range opts {
		options[k] = v
	}

	for k, v := range opts {
		opt, ok := optDefs[k]
		if !ok {
			continue
		}

		kind := reflect.TypeOf(v).Kind()
		if kind != opt.Type() {
			if str, ok := v.(string); ok {
				val, err := opt.Parse(str)
				if err != nil {
					value := fmt.Sprintf("value %q", v)
					if len(str) == 0 {
						value = "empty value"
					}
					return options, fmt.Errorf("could not convert %s to type %q (for option %q)",
						value, opt.Type().String(), "-"+k)
				}
				options[k] = val

			} else {
				return options, fmt.Errorf("option %q should be type %q, but got type %q",
					k, opt.Type().String(), kind.String())
			}
		}

		for _, name := range opt.Names() {
			if _, ok := options[name]; name != k && ok {
				return options, fmt.Errorf("duplicate command options were provided (%q and %q)",
					k, name)
			}
		}
	}

	return options, nil
}

// GetEncoding returns the EncodingType set in a request, falling back to JSON
func GetEncoding(req *Request, def EncodingType) EncodingType {
	switch enc := req.Options[EncLong].(type) {
	case string:
		return EncodingType(enc)
	case EncodingType:
		return enc
	default:
		if def == "" {
			return DefaultOutputEncoding
		}
		return def
	}
}

// FillDefaults fills in default values if option has not been set.
func (req *Request) FillDefaults() error {
	optDefMap, err := req.Root.GetOptions(req.Path)
	if err != nil {
		return err
	}

	optDefs := map[Option]struct{}{}

	for _, optDef := range optDefMap {
		optDefs[optDef] = struct{}{}
	}

Outer:
	for optDef := range optDefs {
		dflt := optDef.Default()
		if dflt == nil {
			// option has no dflt, continue
			continue
		}

		names := optDef.Names()
		for _, name := range names {
			if _, ok := req.Options[name]; ok {
				// option has been set, continue with next option
				continue Outer
			}
		}

		req.Options[optDef.Name()] = dflt
	}

	return nil
}
