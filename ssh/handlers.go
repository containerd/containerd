package ssh

// ChannelHandler handles a new channel request for a connection. Using a
// Server, these handles are run in a separate goroutine.
type ChannelHandler interface {
	ServeChannel(newch ssh.NewChannel) error
}

type ChannelHandlerFn func(newch ssh.NewChannel) error

func (hf ChannelHandlerFn) ServeChannel(newch ssh.NewChannel) error {
	return hf(newch)
}

// AcceptedHandler is a shortcut for calling back from a ChannelHandler that
// filters channels.
type AcceptedHandler interface {
	ServeAccepted(channel ssh.Channel, requests <-chan *ssh.Request) error
}

type AcceptedHandlerFn func(channel ssh.Channel, requests <-chan *ssh.Request) error

func (fn AcceptedHandlerFn) ServeAccepted(channel ssh.Channel, requests <-chan *ssh.Request) error {
	return fn(channel, requests)
}

// SessionsOnly returns a channel handler that only allows channel session
// types. The NewChannel.Accept method will be called and control will be
// passed to the provided AcceptedHandler.
func SessionsOnly(handler AcceptedHandler) ChannelHandler {
	return ChannelHandlerFn(func(newch ssh.NewChannel) error {
		if newch.ChannelType() != "session" {
			return newch.Reject(ssh.UnknownChannelType, "unsupport channel type")
		}

		channel, requests, err := newch.Accept()
		if err != nil {
			return err
		}
		defer channel.Close()

		return handler.ServeAccepted(channel, requests)
	})
}