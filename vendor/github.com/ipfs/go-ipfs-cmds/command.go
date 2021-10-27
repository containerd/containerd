/*
Package commands provides an API for defining and parsing commands.

Supporting nested commands, options, arguments, etc.  The commands
package also supports a collection of marshallers for presenting
output to the user, including text, JSON, and XML marshallers.
*/

package cmds

import (
	"errors"
	"fmt"
	"strings"

	files "github.com/ipfs/go-ipfs-files"

	logging "github.com/ipfs/go-log"
)

// DefaultOutputEncoding defines the default API output encoding.
const DefaultOutputEncoding = JSON

var log = logging.Logger("cmds")

// Function is the type of function that Commands use.
// It reads from the Request, and writes results to the ResponseEmitter.
type Function func(*Request, ResponseEmitter, Environment) error

// PostRunMap is the map used in Command.PostRun.
type PostRunMap map[PostRunType]func(Response, ResponseEmitter) error

// Command is a runnable command, with input arguments and options (flags).
// It can also have Subcommands, to group units of work into sets.
type Command struct {
	// Options defines the flags accepted by the command. Flags on specified
	// on parent commands are inherited by sub commands.
	Options []Option

	// Arguments defines the positional arguments for the command. These
	// arguments can be strings and/or files.
	//
	// The rules for valid arguments are as follows:
	//
	// 1. No required arguments may follow optional arguments.
	// 2. There can be at most one STDIN argument.
	// 3. There can be at most one variadic argument, and it must be last.
	Arguments []Argument

	// PreRun is run before Run. When executing a command on a remote
	// daemon, PreRun is always run in the local process.
	PreRun func(req *Request, env Environment) error

	// Run is the function that processes the request to generate a response.
	// Note that when executing the command over the HTTP API you can only read
	// after writing when using multipart requests. The request body will not be
	// available for reading after the HTTP connection has been written to.
	Run Function

	// PostRun is run after Run, and can transform results returned by run.
	// When executing a command on a remote daemon, PostRun is always run in
	// the local process.
	PostRun PostRunMap

	// Encoders encode results from Run (and/or PostRun) in the desired
	// encoding.
	Encoders EncoderMap

	// Helptext is the command's help text.
	Helptext HelpText

	// External denotes that a command is actually an external binary.
	// fewer checks and validations will be performed on such commands.
	External bool

	// Type describes the type of the output of the Command's Run Function.
	// In precise terms, the value of Type is an instance of the return type of
	// the Run Function.
	//
	// ie. If command Run returns &Block{}, then Command.Type == &Block{}
	Type interface{}

	// Subcommands allow attaching sub commands to a command.
	//
	// Note: A command can specify both a Run function and Subcommands. If
	// invoked with no arguments, or an argument that matches no
	// sub commands, the Run function of the current command will be invoked.
	//
	// Take care when specifying both a Run function and Subcommands. A
	// simple typo in a sub command will invoke the parent command and may
	// end up returning a cryptic error to the user.
	Subcommands map[string]*Command

	// NoRemote denotes that a command cannot be executed in a remote environment
	NoRemote bool

	// NoLocal denotes that a command cannot be executed in a local environment
	NoLocal bool

	// Extra contains a set of other command-specific parameters
	Extra *Extra
}

// Extra is a set of tag information for a command
type Extra struct {
	m map[interface{}]interface{}
}

func (e *Extra) SetValue(key, value interface{}) *Extra {
	if e == nil {
		e = &Extra{}
	}
	if e.m == nil {
		e.m = make(map[interface{}]interface{})
	}
	e.m[key] = value
	return e
}

func (e *Extra) GetValue(key interface{}) (interface{}, bool) {
	if e == nil || e.m == nil {
		return nil, false
	}
	val, found := e.m[key]
	return val, found
}

var (
	// ErrNotCallable signals a command that cannot be called.
	ErrNotCallable = ClientError("this command cannot be called directly; try one of its subcommands.")

	// ErrNoFormatter signals that the command can not be formatted.
	ErrNoFormatter = ClientError("this command cannot be formatted to plain text")

	// ErrIncorrectType signales that the commands returned a value with unexpected type.
	ErrIncorrectType = errors.New("the command returned a value with a different type than expected")
)

// Call invokes the command for the given Request
func (c *Command) Call(req *Request, re ResponseEmitter, env Environment) {
	var closeErr error

	err := c.call(req, re, env)
	if err != nil {
		log.Debugf("error occured in call, closing with error: %s", err)
	}

	closeErr = re.CloseWithError(err)
	// ignore double close errors
	if closeErr != nil && closeErr != ErrClosingClosedEmitter {
		log.Errorf("error closing ResponseEmitter: %s", closeErr)
	}
}

func (c *Command) call(req *Request, re ResponseEmitter, env Environment) error {
	cmd, err := c.Get(req.Path)
	if err != nil {
		log.Errorf("could not get cmd from path %q: %q", req.Path, err)
		return err
	}

	if cmd.Run == nil {
		log.Errorf("returned command has nil Run function")
		return err
	}

	err = cmd.CheckArguments(req)
	if err != nil {
		log.Errorf("CheckArguments returned an error for path %q: %q", req.Path, err)
		return err
	}

	return cmd.Run(req, re, env)
}

// Resolve returns the subcommands at the given path
// The returned set of subcommands starts with this command and therefore is always at least size 1
func (c *Command) Resolve(pth []string) ([]*Command, error) {
	cmds := make([]*Command, len(pth)+1)
	cmds[0] = c

	cmd := c
	for i, name := range pth {
		cmd = cmd.Subcommands[name]

		if cmd == nil {
			pathS := strings.Join(pth[:i], "/")
			return nil, fmt.Errorf("undefined command: %q", pathS)
		}

		cmds[i+1] = cmd
	}

	return cmds, nil
}

// Get resolves and returns the Command addressed by path
func (c *Command) Get(path []string) (*Command, error) {
	cmds, err := c.Resolve(path)
	if err != nil {
		return nil, err
	}
	return cmds[len(cmds)-1], nil
}

// GetOptions returns the options in the given path of commands
func (c *Command) GetOptions(path []string) (map[string]Option, error) {
	options := make([]Option, 0, len(c.Options))

	cmds, err := c.Resolve(path)
	if err != nil {
		return nil, err
	}

	for _, cmd := range cmds {
		options = append(options, cmd.Options...)
	}

	optionsMap := make(map[string]Option)
	for _, opt := range options {
		for _, name := range opt.Names() {
			if _, found := optionsMap[name]; found {
				return nil, fmt.Errorf("option name %q used multiple times", name)
			}

			optionsMap[name] = opt
		}
	}

	return optionsMap, nil
}

// DebugValidate checks if the command tree is well-formed.
//
// This operation is slow and should be called from tests only.
func (c *Command) DebugValidate() map[string][]error {
	errs := make(map[string][]error)
	var visit func(path string, cm *Command)

	liveOptions := make(map[string]struct{})
	visit = func(path string, cm *Command) {
		expectOptional := false
		for i, argDef := range cm.Arguments {
			// No required arguments after optional arguments.
			if argDef.Required {
				if expectOptional {
					errs[path] = append(errs[path], fmt.Errorf("required argument %s after optional arguments", argDef.Name))
					return
				}
			} else {
				expectOptional = true
			}

			// variadic arguments and those supporting stdin must be last
			if (argDef.Variadic || argDef.SupportsStdin) && i != len(cm.Arguments)-1 {
				errs[path] = append(errs[path], fmt.Errorf("variadic and/or optional argument %s must be last", argDef.Name))
			}
		}

		var goodOptions []string
		for _, option := range cm.Options {
			for _, name := range option.Names() {
				if _, ok := liveOptions[name]; ok {
					errs[path] = append(errs[path], fmt.Errorf("duplicate option name %s", name))
				} else {
					goodOptions = append(goodOptions, name)
					liveOptions[name] = struct{}{}
				}
			}
		}
		for scName, sc := range cm.Subcommands {
			visit(fmt.Sprintf("%s/%s", path, scName), sc)
		}

		for _, name := range goodOptions {
			delete(liveOptions, name)
		}
	}
	visit("", c)
	if len(errs) == 0 {
		errs = nil
	}
	return errs
}

// CheckArguments checks that we have all the required string arguments, loading
// any from stdin if necessary.
func (c *Command) CheckArguments(req *Request) error {
	if len(c.Arguments) == 0 {
		return nil
	}

	lastArg := c.Arguments[len(c.Arguments)-1]
	if req.bodyArgs == nil && // check this as we can end up calling CheckArguments multiple times. See #80.
		lastArg.SupportsStdin &&
		lastArg.Type == ArgString &&
		req.Files != nil {

		it := req.Files.Entries()
		if it.Next() {
			req.bodyArgs = newArguments(files.FileFromEntry(it))
			// Can't pass files and stdin arguments.
			req.Files = nil
		} else {
			if it.Err() != nil {
				return it.Err()
			}
		}
	}

	// iterate over the arg definitions
	requiredStringArgs := 0 // number of required string arguments
	for _, argDef := range req.Command.Arguments {
		// Is this a string?
		if argDef.Type != ArgString {
			// No, skip it.
			continue
		}

		// No more required arguments?
		if !argDef.Required {
			// Yes, we're all done.
			break
		}
		requiredStringArgs++

		// Do we have enough string arguments?
		if requiredStringArgs <= len(req.Arguments) {
			// all good
			continue
		}

		// Can we get it from stdin?
		if argDef.SupportsStdin && req.bodyArgs != nil {
			if req.bodyArgs.Scan() {
				// Found it!
				req.Arguments = append(req.Arguments, req.bodyArgs.Argument())
				continue
			}
			if err := req.bodyArgs.Err(); err != nil {
				return err
			}
			// No, just missing.
		}
		return fmt.Errorf("argument %q is required", argDef.Name)
	}

	return nil
}

type CommandVisitor func(*Command)

// Walks tree of all subcommands (including this one)
func (c *Command) Walk(visitor CommandVisitor) {
	visitor(c)
	for _, sub := range c.Subcommands {
		sub.Walk(visitor)
	}
}

func (c *Command) ProcessHelp() {
	c.Walk(func(cm *Command) {
		ht := &cm.Helptext
		if len(ht.LongDescription) == 0 {
			ht.LongDescription = ht.ShortDescription
		}
	})
}

func ClientError(msg string) error {
	return &Error{Code: ErrClient, Message: msg}
}
