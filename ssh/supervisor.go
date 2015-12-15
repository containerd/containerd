package ssh

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/docker/containerd"
	"golang.org/x/crypto/ssh"
)

func SupervisorCommands(supervisor containerd.Supervisor) ChannnelHandler {
	return SessionsOnly(AcceptedHandlerFn(func(channel ssh.Channel, requests <-chan *ssh.Request) error {
		var command struct {
			Name string
			Args []string

			pty bool
		}

		ready := make(chan struct{})
		errs := make(chan error)

		go func(requests <-chan *ssh.Request) {
			// TODO(stevvooe): Most of this is common to an "interactive"
			// sessions. Interactive sessions take "env", "pty-req" and other
			// commands until an "exec", "shell" or "subsystem" command is
			// received.

			var completed bool
			for req := range requests {
				var ok bool
				switch req.Type {
				case "pty-req":
					if completed {
						break
					}

					// TODO(stevvooe): Correctly implemnt pty support here.
					// Effectively, channel needs to be hooked up to the
					// psuedo terminal.
					command.pty = true
					ok = true
				case "shell":
					// ignore requests to shell for now.

					// TODO(stevvooe): Organize supported commands into shell-
					// like interaction for debugging containers.
				case "exec":
					ok = true

					var execRequest struct {
						Command string
					}

					if err := ssh.Unmarshal(req.Payload, &execRequest); err != nil {
						log.Println("error unmarshaling exec:", err)
						return
					}

					parts := strings.Fields(execRequest.Command)

					command.Name = parts[0]
					command.Args = parts[1:]
					completed = true
					ready <- struct{}{}
				}

				if err := req.Reply(ok, nil); err != nil {
					log.Println("reply-failure", err)
					errs <- err // this will block until
				}
			}
		}(requests)

		for {
			select {
			case <-ready:
				if command.Name != "attach" {
					return fmt.Errorf("unsupported command: %v", command.Name)
				}

				id := command.Args[0]
				pid := 1
				if len(command.Args) > 1 {
					pid, err = strconv.Atoi(command.Args[0])
					if err != nil {
						return fmt.Errorf("invalid argument for pid: %v", err)
					}
				} else {
					pid = 1
				}

				event := containerd.NewEvent(containerd.AttachEventType)
				event.ID = id
				event.PID = pid

				supervisor.SendEvent(event)

				if err := <-event.Err; err != nil {
					return err
				}

				// TODO(stevvooe): Use the event.IO field to hook up the IO
				// streams from the process to the ssh channel. Several
				// goroutines will need to be placed here to make this happen
				// correctly. It may be better to have IO be the opposite of
				// what it currently is, and submit a set of streams to be
				// attached, as arguments. This will give the runtime better
				// control over the streams, albeit, we'd have to study
				// whether this will cause issues with resource cleanup.
			case err := <-errs:
				return err
			}
		}

		return nil
	}))
}
