package cmds

import (
	"context"
)

type Executor interface {
	Execute(req *Request, re ResponseEmitter, env Environment) error
}

// Environment is the environment passed to commands.
type Environment interface{}

// MakeEnvironment takes a context and the request to construct the environment
// that is passed to the command's Run function.
// The user can define a function like this to pass it to cli.Run.
type MakeEnvironment func(context.Context, *Request) (Environment, error)

// MakeExecutor takes the request and environment variable to construct the
// executor that determines how to call the command - i.e. by calling Run or
// making an API request to a daemon.
// The user can define a function like this to pass it to cli.Run.
type MakeExecutor func(*Request, interface{}) (Executor, error)

func NewExecutor(root *Command) Executor {
	return &executor{
		root: root,
	}
}

type executor struct {
	root *Command
}

func (x *executor) Execute(req *Request, re ResponseEmitter, env Environment) error {
	cmd := req.Command

	if cmd.Run == nil {
		return ErrNotCallable
	}

	err := cmd.CheckArguments(req)
	if err != nil {
		return err
	}

	if cmd.PreRun != nil {
		err = cmd.PreRun(req, env)
		if err != nil {
			return err
		}
	}

	// contains the error returned by PostRun
	errCh := make(chan error, 1)
	if cmd.PostRun != nil {
		if typer, ok := re.(interface {
			Type() PostRunType
		}); ok && cmd.PostRun[typer.Type()] != nil {
			var (
				res   Response
				lower = re
			)

			re, res = NewChanResponsePair(req)

			go func() {
				defer close(errCh)
				errCh <- lower.CloseWithError(cmd.PostRun[typer.Type()](res, lower))
			}()
		}
	} else {
		// not using this channel today
		close(errCh)
	}

	runCloseErr := re.CloseWithError(cmd.Run(req, re, env))
	postCloseErr := <-errCh
	switch runCloseErr {
	case ErrClosingClosedEmitter, nil:
	default:
		return runCloseErr
	}
	switch postCloseErr {
	case ErrClosingClosedEmitter, nil:
	default:
		return postCloseErr
	}
	return nil
}
