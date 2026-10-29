//go:build linux

package apparmor

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultProfileAllowsStackedPeers(t *testing.T) {
	var buf bytes.Buffer
	err := generate(&data{Name: "cri-containerd.apparmor.d"}, &buf)
	assert.NoError(t, err)
	prof := buf.String()
	assert.Contains(t, prof, "signal (send,receive) peer=cri-containerd.apparmor.d//&*,")
	assert.Contains(t, prof, "ptrace (trace,tracedby,read,readby) peer=cri-containerd.apparmor.d//&*,")
	assert.Contains(t, prof, "signal (send,receive) peer=cri-containerd.apparmor.d,")
}

func TestCleanProfileName(t *testing.T) {
	assert.Equal(t, cleanProfileName(""), "unconfined")
	assert.Equal(t, cleanProfileName("unconfined"), "unconfined")
	assert.Equal(t, cleanProfileName("unconfined (enforce)"), "unconfined")
	assert.Equal(t, cleanProfileName("docker-default"), "docker-default")
	assert.Equal(t, cleanProfileName("foo"), "foo")
	assert.Equal(t, cleanProfileName("foo (enforce)"), "foo")
}
