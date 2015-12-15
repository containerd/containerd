package ssh

// Server implements a simple ssh server for use with serving attach sessions.
type Server struct {
	Config  *ssh.ServerConfig
	Handler ChannelHandler

	log Logger
}

func NewServer(config *ssh.ServerConfig, handler ChannelHandler) *Server {
	return &Server{
		Config:  config,
		Handler: handler,
	}
}

func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			// TODO(stevvooe): Handle temporary errors here.
			return err
		}

		go func(conn net.Conn) {
			if err := s.ServeConn(conn); err != nil {
				if err != io.EOF && !isDisconnect(err) {
					log.Printf("(*Server).ServeConn: %#v", err)
				}
			}
		}(conn)
	}

	return nil
}

// ServeConn serves the provided connection, closing on return of the
// function. An error will be returned if the connection exits with an error.
func (s *Server) ServeConn(conn net.Conn) (err error) {
	defer func() {
		cerr := conn.Close()
		if err == nil {
			err = cerr
		}
	}()

	sconn, channels, requests, err := ssh.NewServerConn(conn, s.Config)
	if err != nil {
		return err
	}
	defer sconn.Close()

	log.Println("(*Server).ServeConn:", sconn.RemoteAddr())
	go ssh.DiscardRequests(requests)
	go func(channels <-chan ssh.NewChannel) {
		if err := s.serveChannels(channels); err != nil {
			log.Println("(*Server).serveChannels:", err)
		}
	}(channels)

	return sconn.Wait()
}

func (s *Server) serveChannels(channels <-chan ssh.NewChannel) error {
	for newch := range channels {
		go func() {
			if err := s.Handler.ServeChannel(newch); err != nil {
				log.Println("(*Server).ServeChannel:", err)
			}
		}()
	}

	return nil
}

// isDisconnect returns true if there was a disconnect error.
func isDisconnect(err error) bool {
	// NOTE(stevvooe): Icky! String matching since these aren't really
	// exported in any way from crypto/ssh.
	return strings.Contains(err.Error(), "ssh: disconnect reason")
}