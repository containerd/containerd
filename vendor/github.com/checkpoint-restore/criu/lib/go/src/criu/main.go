package criu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/golang/protobuf/proto"
	"github.com/checkpoint-restore/criu/lib/go/src/rpc"
)

type Criu struct {
	swrk_cmd *exec.Cmd
	swrk_sk  *os.File
}

func MakeCriu() *Criu {
	return &Criu{}
}

func (c *Criu) Prepare() error {
	fds, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		return err
	}

	cln := os.NewFile(uintptr(fds[0]), "criu-xprt-cln")
	syscall.CloseOnExec(fds[0])
	srv := os.NewFile(uintptr(fds[1]), "criu-xprt-srv")
	defer srv.Close()

	args := []string{"swrk", strconv.Itoa(fds[1])}
	cmd := exec.Command("criu", args...)

	err = cmd.Start()
	if err != nil {
		cln.Close()
		return err
	}

	c.swrk_cmd = cmd
	c.swrk_sk = cln

	return nil
}

func (c *Criu) Cleanup() {
	if c.swrk_cmd != nil {
		c.swrk_sk.Close()
		c.swrk_sk = nil
		c.swrk_cmd.Wait()
		c.swrk_cmd = nil
	}
}

func (c *Criu) sendAndRecv(req_b []byte) ([]byte, int, error) {
	cln := c.swrk_sk
	_, err := cln.Write(req_b)
	if err != nil {
		return nil, 0, err
	}

	resp_b := make([]byte, 2*4096)
	n, err := cln.Read(resp_b)
	if err != nil {
		return nil, 0, err
	}

	return resp_b, n, nil
}

func (c *Criu) doSwrk(req_type rpc.CriuReqType, opts *rpc.CriuOpts, nfy CriuNotify) error {
	req := rpc.CriuReq{
		Type: &req_type,
		Opts: opts,
	}

	if nfy != nil {
		opts.NotifyScripts = proto.Bool(true)
	}

	if c.swrk_cmd == nil {
		err := c.Prepare()
		if err != nil {
			return err
		}

		defer c.Cleanup()
	}

	for {
		req_b, err := proto.Marshal(&req)
		if err != nil {
			return err
		}

		resp_b, resp_s, err := c.sendAndRecv(req_b)
		if err != nil {
			return err
		}

		resp := &rpc.CriuResp{}
		err = proto.Unmarshal(resp_b[:resp_s], resp)
		if err != nil {
			return err
		}

		if !resp.GetSuccess() {
			return fmt.Errorf("operation failed (msg:%s err:%d)",
				resp.GetCrErrmsg(), resp.GetCrErrno())
		}

		resp_type := resp.GetType()
		if resp_type == req_type {
			break
		}
		if resp_type != rpc.CriuReqType_NOTIFY {
			return errors.New("unexpected responce")
		}
		if nfy == nil {
			return errors.New("unexpected notify")
		}

		notify := resp.GetNotify()
		switch notify.GetScript() {
		case "pre-dump":
			err = nfy.PreDump()
		case "post-dump":
			err = nfy.PostDump()
		case "pre-restore":
			err = nfy.PreRestore()
		case "post-restore":
			err = nfy.PostRestore(notify.GetPid())
		case "network-lock":
			err = nfy.NetworkLock()
		case "network-unlock":
			err = nfy.NetworkUnlock()
		case "setup-namespaces":
			err = nfy.SetupNamespaces(notify.GetPid())
		case "post-setup-namespaces":
			err = nfy.PostSetupNamespaces()
		case "post-resume":
			err = nfy.PostResume()
		default:
			err = nil
		}

		if err != nil {
			return err
		}

		req = rpc.CriuReq{
			Type:          &resp_type,
			NotifySuccess: proto.Bool(true),
		}
	}

	return nil
}

func (c *Criu) Dump(opts rpc.CriuOpts, nfy CriuNotify) error {
	return c.doSwrk(rpc.CriuReqType_DUMP, &opts, nfy)
}

func (c *Criu) Restore(opts rpc.CriuOpts, nfy CriuNotify) error {
	return c.doSwrk(rpc.CriuReqType_RESTORE, &opts, nfy)
}

func (c *Criu) PreDump(opts rpc.CriuOpts, nfy CriuNotify) error {
	return c.doSwrk(rpc.CriuReqType_PRE_DUMP, &opts, nfy)
}

func (c *Criu) StartPageServer(opts rpc.CriuOpts) error {
	return c.doSwrk(rpc.CriuReqType_PAGE_SERVER, &opts, nil)
}
