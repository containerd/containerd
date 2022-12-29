//go:build linux

package apparmor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanProfileName(t *testing.T) {
	assert.Equal(t, cleanProfileName(""), "unconfined")
	assert.Equal(t, cleanProfileName("unconfined"), "unconfined")
	assert.Equal(t, cleanProfileName("unconfined (enforce)"), "unconfined")
	assert.Equal(t, cleanProfileName("docker-default"), "docker-default")
	assert.Equal(t, cleanProfileName("foo"), "foo")
	assert.Equal(t, cleanProfileName("foo (enforce)"), "foo")
}
