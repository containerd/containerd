package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/docker/containerd/api/services/shim"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

func newShimClient(root, id string) (*shimClient, error) {
	if err := os.Mkdir(root, 0700); err != nil {
		return nil, errors.Wrap(err, "failed to create shim working dir")
	}

	cmd := exec.Command("containerd-shim")
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(root)
		return nil, errors.Wrapf(err, "failed to start shim")
	}

	socket := filepath.Join(root, "shim.sock")
	sc, err := connectToShim(socket)
	if err != nil {
		syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		os.RemoveAll(root)
		return nil, err
	}

	s := &shimClient{
		ShimClient: sc,
		shimCmd:    cmd,
		syncCh:     make(chan struct{}),
		root:       root,
		id:         id,
	}
	go func() {
		cmd.Wait()
		close(s.syncCh)
	}()

	return s, nil
}

func loadShimClient(root, id string) (*shimClient, error) {
	socket := filepath.Join(root, "shim.sock")
	client, err := connectToShim(socket)
	if err != nil {
		// TODO: failed to connect to the shim, check if it's alive
		//   - if it is kill it
		//   - in both case call runc killall and runc delete on the id
		return nil, err
	}

	resp, err := client.State(context.Background(), &shim.StateRequest{}, grpc.FailFast(false))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch state for container %s", id)
	}

	return &shimClient{
		ShimClient: client,
		root:       root,
		id:         id,
		initPid:    resp.InitPid,
	}, nil
}

type shimClient struct {
	shim.ShimClient
	shimCmd *exec.Cmd
	syncCh  chan struct{}
	root    string
	id      string
	initPid uint32
}

func (s *shimClient) stop() {
	if s.shimCmd != nil {
		select {
		case <-s.syncCh:
		default:
			syscall.Kill(s.shimCmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-s.syncCh:
			case <-time.After(10 * time.Second):
				syscall.Kill(s.shimCmd.Process.Pid, syscall.SIGKILL)
			}
		}
	}
	os.RemoveAll(s.root)
}

func (s *shimClient) readRuntimeLogEntries(lastEntries int) ([]map[string]interface{}, error) {
	f, err := os.Open(filepath.Join(s.root, "log.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseRuntimeLog(f, lastEntries)
}

// parseRuntimeLog parses log.json.
// If lastEntries is greater than 0, only some last entries are returned.
func parseRuntimeLog(r io.Reader, lastEntries int) ([]map[string]interface{}, error) {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	if lastEntries > 0 {
		lines = lines[len(lines)-lastEntries-1 : len(lines)]
	}
	var entries []map[string]interface{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, errors.Wrapf(err, "cannot unmarshal JSON string %q", line)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func connectToShim(socket string) (shim.ShimClient, error) {
	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithTimeout(100 * time.Second)}
	dialOpts = append(dialOpts,
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", socket, timeout)
		}),
	)
	conn, err := grpc.Dial(fmt.Sprintf("unix://%s", socket), dialOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to connect to shim via \"%s\"", fmt.Sprintf("unix://%s", socket))
	}
	return shim.NewShimClient(conn), nil
}
