/*
   Copyright The containerd Authors.

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

package rootquota

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/events/exchange"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

/*
   Rootquota plugin is userd to limit disk size of container rootfs.
   Required:
	 1.snapshot data dir, usually is container root dir, must xfs filesystem
     2.snapshoter is overlayfs
	 3.linux/unix os
   The current version dose not supported in windows.
*/

const RootQuotaPluginType plugin.Type = "io.containerd.rootquota.v1"

// Register local root quota plugin
func init() {
	registry.Register(&plugin.Registration{
		Type: RootQuotaPluginType,
		ID:   "xfs",
		Requires: []plugin.Type{
			plugins.ServicePlugin,
			plugins.EventPlugin,
			plugins.MetadataPlugin,
		},
		Config: defaultConfig(),
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			i, err := initFunc(ic)
			if err != nil {
				return nil, fmt.Errorf("%s xfs init err: %w", RootQuotaPluginType, err)
			}
			return i, nil
		},
	})
}

func defaultConfig() *rootquotaConfig {
	return &rootquotaConfig{
		RootfsQuota: "50G",
		Disable:     false,
	}
}

type rootquotaConfig struct {
	//rootfs disk size, default value 50G.
	RootfsQuota string `toml:"rootfs_quota"`
	//disable = true means disable the plugin, default value false.
	Disable bool `toml:"disable"`
}

func initFunc(ic *plugin.InitContext) (interface{}, error) {
	config := ic.Config.(*rootquotaConfig)
	log.G(ic.Context).Debugf("get rootquotaConfig: %+v", config)
	if config.Disable {
		return nil, nil
	}
	regex := regexp.MustCompile(`\d+[gmGM]{1}`)
	if !regex.MatchString(config.RootfsQuota) {
		return nil, fmt.Errorf("plugin configuration rootfs_quota invalid. e.g. 10G or 500M")
	}
	cmd := exec.Command("xfs_quota", "-V")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.G(ic.Context).Errorf("xfs_quota cmd err: %s, cmd output: %s\n", err, output)
		return nil, fmt.Errorf("plugin required xfsprogs, please install xfsprogs package")
	}
	rootPath := ic.Properties[plugins.PropertyRootDir]
	mountPoint, err := mountPointCheck(rootPath)
	if err != nil {
		return nil, fmt.Errorf("plugin check containerd root path failed, path %s, err: %w", rootPath, err)
	}
	ss, err := ic.GetByID(plugins.ServicePlugin, services.SnapshotsService)
	if err != nil {
		return nil, fmt.Errorf("not fount services.SnapshotsService plugin: %w", err)
	}
	ms := ss.(map[string]snapshots.Snapshotter)
	var overlayfsSS snapshots.Snapshotter
	for s, snapshotter := range ms {
		if s == "overlayfs" {
			overlayfsSS = snapshotter
			break
		}
	}
	if overlayfsSS == nil {
		return nil, fmt.Errorf("not fount overlayfs snapshotter")
	}
	eventPlugin, err := ic.GetByID(plugins.EventPlugin, "exchange")
	if err != nil {
		return nil, fmt.Errorf("not fount event.exchange plugin: %w", err)
	}
	m, err := ic.GetSingle(plugins.MetadataPlugin)
	if err != nil {
		return nil, fmt.Errorf("not fount metadata plugin: %w", err)
	}
	containerStore := metadata.NewContainerStore(m.(*metadata.DB))
	err = os.MkdirAll(rootPath, 0755)
	if err != nil {
		return nil, fmt.Errorf("plugin mk data dir err: %w", err)
	}
	datafile := filepath.Join(rootPath, "datafile")
	var wg sync.WaitGroup
	wg.Add(2)
	var index = make(map[string]string)
	var indexReload map[string]string
	service := &rootQuotaService{
		size:           config.RootfsQuota,
		mountPoint:     mountPoint,
		events:         eventPlugin.(*exchange.Exchange),
		snapshotter:    overlayfsSS,
		containerStore: containerStore,
		datafile:       datafile,
		cache: cache{
			rwlock: sync.RWMutex{},
			index:  index,
		},
	}
	go func() {
		defer wg.Done()
		file, errOF := os.OpenFile(datafile, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
		if errOF != nil && !strings.HasSuffix(errOF.Error(), "file exists") {
			err = fmt.Errorf("plugin open datafile err: %w", err)
			return
		}
		defer file.Close()
		indexReload, err = service.reloadDatafile()
	}()
	go func() {
		defer wg.Done()
		err = service.reloadContainers(ic.Context)
	}()
	wg.Wait()
	if err != nil {
		return nil, err
	}
	go func() {
		for key, value := range indexReload {
			if _, ok := index[key]; !ok {
				service.deleteQuotaWhenInit(ic.Context, key, value)
			}
		}
	}()
	go service.Run(ic.Context)
	return service, nil
}

func mountPointCheck(path string) (string, error) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// The format of each line is as follows:
		// device mountpoint fstype options rest
		// /dev/sda4 /var/lib/containerd xfs rw,relatime,attr2,inode64,logbufs=8,logbsize=32k,prjquota 0 0
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) >= 4 && strings.HasPrefix(path, parts[1]) && parts[2] == "xfs" && strings.Contains(parts[3], "prjquota") {
			return parts[1], nil
		}
	}
	return "", fmt.Errorf("No suitable mount point found, want xfs and project quota mount point")
}
