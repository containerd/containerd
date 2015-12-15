package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"log"

	"golang.org/x/crypto/ssh"
)

// NewSimpleServerConfig returns a new, simple but very insecure ssh server
// configuration. It provides a no client auth implementation, with auto-
// generated host keys. Run your client with `-o "UserKnownHostsFile
// /dev/null"` to minimize annoyance. This should be removed before any real
// release.
func NewSimpleServerConfig() *ssh.ServerConfig {
	config := ssh.ServerConfig{
		NoClientAuth: true,
	}

	pkr, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Panicln(err)
	}
	pk, err := ssh.NewSignerFromSigner(pkr)
	if err != nil {
		log.Panicln(err)
	}
	config.AddHostKey(pk)

	return &config
}
