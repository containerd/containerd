package cmds

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Types of Command options
const (
	Invalid = reflect.Invalid
	Bool    = reflect.Bool
	Int     = reflect.Int
	Uint    = reflect.Uint
	Int64   = reflect.Int64
	Uint64  = reflect.Uint64
	Float   = reflect.Float64
	String  = reflect.String
	Strings = reflect.Array
)

type OptMap map[string]interface{}

// Option is used to specify a field that will be provided by a consumer
type Option interface {
	Name() string    // the main name of the option
	Names() []string // a list of unique names matched with user-provided flags

	Type() reflect.Kind  // value must be this type
	Description() string // a short string that describes this option

	WithDefault(interface{}) Option // sets the default value of the option
	Default() interface{}

	Parse(str string) (interface{}, error)
}

type option struct {
	names       []string
	kind        reflect.Kind
	description string
	defaultVal  interface{}
}

func (o *option) Name() string {
	return o.names[0]
}

func (o *option) Names() []string {
	return o.names
}

func (o *option) Type() reflect.Kind {
	return o.kind
}

func (o *option) Description() string {
	if len(o.description) == 0 {
		return ""
	}
	if !strings.HasSuffix(o.description, ".") {
		o.description += "."
	}
	if o.defaultVal != nil {
		if strings.Contains(o.description, "<<default>>") {
			return strings.Replace(o.description, "<<default>>",
				fmt.Sprintf("Default: %v.", o.defaultVal), -1)
		} else {
			return fmt.Sprintf("%s Default: %v.", o.description, o.defaultVal)
		}
	}
	return o.description
}

type converter func(string) (interface{}, error)

var converters = map[reflect.Kind]converter{
	Bool: func(v string) (interface{}, error) {
		if v == "" {
			return true, nil
		}
		v = strings.ToLower(v)

		return strconv.ParseBool(v)
	},
	Int: func(v string) (interface{}, error) {
		val, err := strconv.ParseInt(v, 0, 32)
		if err != nil {
			return nil, err
		}
		return int(val), err
	},
	Uint: func(v string) (interface{}, error) {
		val, err := strconv.ParseUint(v, 0, 32)
		if err != nil {
			return nil, err
		}
		return uint(val), err
	},
	Int64: func(v string) (interface{}, error) {
		val, err := strconv.ParseInt(v, 0, 64)
		if err != nil {
			return nil, err
		}
		return val, err
	},
	Uint64: func(v string) (interface{}, error) {
		val, err := strconv.ParseUint(v, 0, 64)
		if err != nil {
			return nil, err
		}
		return val, err
	},
	Float: func(v string) (interface{}, error) {
		return strconv.ParseFloat(v, 64)
	},
	String: func(v string) (interface{}, error) {
		return v, nil
	},
	Strings: func(v string) (interface{}, error) {
		return v, nil
	},
}

func (o *option) Parse(v string) (interface{}, error) {
	conv, ok := converters[o.Type()]
	if !ok {
		return nil, fmt.Errorf("option %q takes %s arguments, but was passed %q", o.Name(), o.Type(), v)
	}

	return conv(v)
}

// constructor helper functions
func NewOption(kind reflect.Kind, names ...string) Option {
	var desc string

	if len(names) >= 2 {
		desc = names[len(names)-1]
		names = names[:len(names)-1]
	}

	return &option{
		names:       names,
		kind:        kind,
		description: desc,
	}
}

func (o *option) WithDefault(v interface{}) Option {
	o.defaultVal = v
	return o
}

func (o *option) Default() interface{} {
	return o.defaultVal
}

// TODO handle description separately. this will take care of the panic case in
// NewOption

// For all func {Type}Option(...string) functions, the last variadic argument
// is treated as the description field.

func BoolOption(names ...string) Option {
	return NewOption(Bool, names...)
}
func IntOption(names ...string) Option {
	return NewOption(Int, names...)
}
func UintOption(names ...string) Option {
	return NewOption(Uint, names...)
}
func Int64Option(names ...string) Option {
	return NewOption(Int64, names...)
}
func Uint64Option(names ...string) Option {
	return NewOption(Uint64, names...)
}
func FloatOption(names ...string) Option {
	return NewOption(Float, names...)
}
func StringOption(names ...string) Option {
	return NewOption(String, names...)
}
func StringsOption(names ...string) Option {
	return NewOption(Strings, names...)
}
