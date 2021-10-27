package routing

// Option is a single routing option.
type Option func(opts *Options) error

// Options is a set of routing options
type Options struct {
	// Allow expired values.
	Expired bool
	Offline bool
	// Other (ValueStore implementation specific) options.
	Other map[interface{}]interface{}
}

// Apply applies the given options to this Options
func (opts *Options) Apply(options ...Option) error {
	for _, o := range options {
		if err := o(opts); err != nil {
			return err
		}
	}
	return nil
}

// ToOption converts this Options to a single Option.
func (opts *Options) ToOption() Option {
	return func(nopts *Options) error {
		*nopts = *opts
		if opts.Other != nil {
			nopts.Other = make(map[interface{}]interface{}, len(opts.Other))
			for k, v := range opts.Other {
				nopts.Other[k] = v
			}
		}
		return nil
	}
}

// Expired is an option that tells the routing system to return expired records
// when no newer records are known.
var Expired Option = func(opts *Options) error {
	opts.Expired = true
	return nil
}

// Offline is an option that tells the routing system to operate offline (i.e., rely on cached/local data only).
var Offline Option = func(opts *Options) error {
	opts.Offline = true
	return nil
}
