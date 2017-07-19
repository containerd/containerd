// +build !windows

package containerd

const (
	defaultRoot    = "/var/lib/containerd-test"
	defaultAddress = "/run/containerd-test/containerd.sock"
	testImage      = "docker.io/library/alpine:latest"
)

func platformTestSetup(client *Client) error {
	return nil
}
