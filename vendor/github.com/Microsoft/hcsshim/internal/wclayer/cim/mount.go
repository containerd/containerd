package cim

import (
	"context"
	"fmt"
	"os"
	"sync"

	winio "github.com/Microsoft/go-winio"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"

	mountmanager "github.com/containerd/containerd/api/services/mounts/v1"
	cimmountapi "github.com/containerd/containerd/mountmanager/cimfs"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
)

// Functions in this file are used for mounting the cim on the host (i.e host where this shim process is
// running) This is mostly done for Argon. For Xenon the cims are mounted inside the uvm, the function that do
// that are inside internal/uvm/mounts.go

const (
	ttrpcAddressEnv = "TTRPC_ADDRESS"
)

var ttrpcAddress string

func init() {
	ttrpcAddress = os.Getenv(ttrpcAddressEnv)
	// TODO(ambarve): make a connection here and reuse that during the lifetime of the shim Figure out how
	// to properly close that connection on shim shutdown
}

// a cache of cim layer to its mounted volume - The mount manager plugin currently doesn't have an option of
// querying a mounted cim to get the volume at which it is mounted, so we maintain a cache of that here
var (
	cimMounts       map[string]string = make(map[string]string)
	cimMountMapLock sync.Mutex
)

func getMountManagerClient(ctx context.Context) (mountmanager.TTRPCMountsService, error) {
	c, err := winio.DialPipeContext(ctx, ttrpcAddress)
	if err != nil {
		return nil, fmt.Errorf("dial containerd ttrpc pipe %s: %w", ttrpcAddress, err)
	}
	return mountmanager.NewTTRPCMountsClient(ttrpc.NewClient(c)), nil
}

func sendMountRequest(ctx context.Context, req *cimmountapi.CimMountRequest) (*cimmountapi.CimMountResponse, error) {
	reqData, err := protobuf.MarshalAnyToProto(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	client, err := getMountManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("get client to mount manager: %w", err)
	}

	mreq := mountmanager.MountRequest{
		PluginName: cimmountapi.PluginName,
		Data:       reqData,
	}

	mresp, err := client.Mount(ctx, &mreq)
	if err != nil {
		return nil, err
	}

	respData, err := typeurl.UnmarshalAny(mresp.Data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	resp, ok := respData.(*cimmountapi.CimMountResponse)
	if !ok {
		return nil, fmt.Errorf("invalid response type: %T", respData)
	}
	return resp, nil
}

func sendUnmountRequest(ctx context.Context, req *cimmountapi.CimUnmountRequest) (*cimmountapi.CimUnmountResponse, error) {
	reqData, err := protobuf.MarshalAnyToProto(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	client, err := getMountManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("get client to mount manager: %w", err)
	}

	mreq := mountmanager.UnmountRequest{
		PluginName: cimmountapi.PluginName,
		Data:       reqData,
	}

	mresp, err := client.Unmount(ctx, &mreq)
	if err != nil {
		return nil, err
	}

	respData, err := typeurl.UnmarshalAny(mresp.Data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	resp, ok := respData.(*cimmountapi.CimUnmountResponse)
	if !ok {
		return nil, fmt.Errorf("invalid response type: %T", respData)
	}
	return resp, nil
}

// mountCimLayerEx mounts the given cim using the given flags by sending a cim mount request to the
func mountCimLayerEx(ctx context.Context, cimPath string, mountFlags uint32) (string, error) {
	req := &cimmountapi.CimMountRequest{
		CimPath: cimPath,
	}

	resp, err := sendMountRequest(ctx, req)
	if err != nil {
		return "", err
	}

	cimMountMapLock.Lock()
	defer cimMountMapLock.Unlock()
	cimMounts[cimPath] = resp.Volume

	return resp.Volume, nil
}

// MountCimLayer mounts the cim at path `cimPath` and returns the mount location of that cim.
// This is done by making a CimMount request to the mountmanager plugin in containerd.
// This method uses the `CimMountFlagCacheFiles` mount flag when mounting the cim, if some other
// mount flag is desired use the `MountWithFlags` method.
func MountCimLayer(ctx context.Context, cimPath string) (string, error) {
	return mountCimLayerEx(ctx, cimPath, hcsschema.CimMountFlagCacheFiles)
}

// Unmount unmounts the cim at mounted at path `volumePath`.
func UnmountCimLayer(ctx context.Context, cimPath string) error {
	req := &cimmountapi.CimUnmountRequest{
		CimPath: cimPath,
	}

	_, err := sendUnmountRequest(ctx, req)
	if err != nil {
		return err
	}

	cimMountMapLock.Lock()
	defer cimMountMapLock.Unlock()
	delete(cimMounts, cimPath)

	return nil
}

// GetCimMountPath returns the volume at which a cim is mounted. If the cim is not mounted returns error
func GetCimMountPath(cimPath string) (string, error) {
	cimMountMapLock.Lock()
	defer cimMountMapLock.Unlock()

	if vol, ok := cimMounts[cimPath]; !ok {
		return "", fmt.Errorf("cim %s not mounted", cimPath)
	} else {
		return vol, nil
	}
}
