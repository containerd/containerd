/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/pkg/errors"
	k8snet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/remotecommand"
	k8scert "k8s.io/client-go/util/cert"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"
	"k8s.io/utils/exec"

	ctrdutil "github.com/containerd/cri/pkg/containerd/util"
)

const (
	// certOrganizationName is the name of this organization, used for certificates etc.
	certOrganizationName = "containerd"
	// certCommonName is the common name of the CRI plugin
	certCommonName = "cri"
)

func newStreamServer(c *criService, addr, port string) (streaming.Server, error) {
	if addr == "" {
		a, err := k8snet.ChooseBindAddress(nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get stream server address")
		}
		addr = a.String()
	}
	config := streaming.DefaultConfig
	config.Addr = net.JoinHostPort(addr, port)
	runtime := newStreamRuntime(c)
	tlsCert, err := newTLSCert()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate tls certificate for stream server")
	}
	config.TLSConfig = &tls.Config{
		Certificates:       []tls.Certificate{tlsCert},
		InsecureSkipVerify: true,
	}
	return streaming.NewServer(config, runtime)
}

type streamRuntime struct {
	c *criService
}

func newStreamRuntime(c *criService) streaming.Runtime {
	return &streamRuntime{c: c}
}

// Exec executes a command inside the container. exec.ExitError is returned if the command
// returns non-zero exit code.
func (s *streamRuntime) Exec(containerID string, cmd []string, stdin io.Reader, stdout, stderr io.WriteCloser,
	tty bool, resize <-chan remotecommand.TerminalSize) error {
	exitCode, err := s.c.execInContainer(ctrdutil.NamespacedContext(), containerID, execOptions{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		tty:    tty,
		resize: resize,
	})
	if err != nil {
		return errors.Wrap(err, "failed to exec in container")
	}
	if *exitCode == 0 {
		return nil
	}
	return &exec.CodeExitError{
		Err:  errors.Errorf("error executing command %v, exit code %d", cmd, *exitCode),
		Code: int(*exitCode),
	}
}

func (s *streamRuntime) Attach(containerID string, in io.Reader, out, err io.WriteCloser, tty bool,
	resize <-chan remotecommand.TerminalSize) error {
	return s.c.attachContainer(ctrdutil.NamespacedContext(), containerID, in, out, err, tty, resize)
}

func (s *streamRuntime) PortForward(podSandboxID string, port int32, stream io.ReadWriteCloser) error {
	if port <= 0 || port > math.MaxUint16 {
		return errors.Errorf("invalid port %d", port)
	}
	return s.c.portForward(podSandboxID, port, stream)
}

// handleResizing spawns a goroutine that processes the resize channel, calling resizeFunc for each
// remotecommand.TerminalSize received from the channel. The resize channel must be closed elsewhere to stop the
// goroutine.
func handleResizing(resize <-chan remotecommand.TerminalSize, resizeFunc func(size remotecommand.TerminalSize)) {
	if resize == nil {
		return
	}

	go func() {
		defer runtime.HandleCrash()

		for {
			size, ok := <-resize
			if !ok {
				return
			}
			if size.Height < 1 || size.Width < 1 {
				continue
			}
			resizeFunc(size)
		}
	}()
}

// newTLSCert returns a tls.certificate loaded from a newly generated
// x509certificate from a newly generated rsa public/private key pair. The
// x509certificate is self signed.
// TODO (mikebrow): replace / rewrite this function to support using CA
// signing of the cetificate. Requires a security plan for kubernetes regarding
// CRI connections / streaming, etc. For example, kubernetes could configure or
// require a CA service and pass a configuration down through CRI.
func newTLSCert() (tls.Certificate, error) {
	fail := func(err error) (tls.Certificate, error) { return tls.Certificate{}, err }
	var years = 1 // duration of certificate

	// Generate new private key
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fail(errors.Wrap(err, "private key cannot be created"))
	}

	// Generate pem block using the private key
	keyPem := pem.EncodeToMemory(&pem.Block{
		Type:  k8scert.RSAPrivateKeyBlockType,
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	// Generate a new random serial number for certificate
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fail(errors.Wrap(err, "failed to generate serial number"))
	}
	hostName, err := os.Hostname()
	if err != nil {
		return fail(errors.Wrap(err, "failed to get hostname"))
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return fail(errors.Wrap(err, "failed to get host IP addresses"))
	}

	// Configure and create new certificate
	tml := x509.Certificate{
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(years, 0, 0),
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s:%s:%s", certOrganizationName, certCommonName, hostName),
			Organization: []string{certOrganizationName},
		},
		BasicConstraintsValid: true,
	}
	for _, addr := range addrs {
		var ip net.IP

		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}

		tml.IPAddresses = append(tml.IPAddresses, ip)
		tml.DNSNames = append(tml.DNSNames, ip.String())
	}

	cert, err := x509.CreateCertificate(rand.Reader, &tml, &tml, &privKey.PublicKey, privKey)
	if err != nil {
		return fail(errors.Wrap(err, "certificate cannot be created"))
	}

	// Generate a pem block with the certificate
	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  k8scert.CertificateBlockType,
		Bytes: cert,
	})

	// Load the tls certificate
	tlsCert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return fail(errors.Wrap(err, "certificate could not be loaded"))
	}

	return tlsCert, nil
}
