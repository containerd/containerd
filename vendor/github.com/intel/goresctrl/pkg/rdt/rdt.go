/*
Copyright 2019 Intel Corporation

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

// Package rdt implements an API for managing Intel® RDT technologies via the
// resctrl pseudo-filesystem of the Linux kernel. It provides flexible
// configuration with a hierarchical approach for easy management of exclusive
// cache allocations.
//
// Goresctrl supports all available RDT technologies, i.e. L2 and L3 Cache
// Allocation (CAT) with Code and Data Prioritization (CDP) and Memory
// Bandwidth Allocation (MBA) plus Cache Monitoring (CMT) and Memory Bandwidth
// Monitoring (MBM).
//
// Basic usage example:
//   rdt.SetLogger(logrus.New())
//
//   if err := rdt.Initialize(""); err != nil {
//   	return fmt.Errorf("RDT not supported: %v", err)
//   }
//
//   if err := rdt.SetConfigFromFile("/path/to/rdt.conf.yaml", false); err != nil {
//   	return fmt.Errorf("RDT configuration failed: %v", err)
//   }
//
//   if cls, ok := rdt.GetClass("my-class"); ok {
//      //  Set PIDs 12345 and 12346 to class "my-class"
//   	if err := cls.AddPids("12345", "12346"); err != nil {
//   		return fmt.Errorf("failed to add PIDs to RDT class: %v", err)
//   	}
//   }
package rdt

import (
	"errors"
	"fmt"
	"io/ioutil"
	stdlog "log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"sigs.k8s.io/yaml"

	grclog "github.com/intel/goresctrl/pkg/log"
	"github.com/intel/goresctrl/pkg/utils"
)

const (
	// RootClassName is the name we use in our config for the special class
	// that configures the "root" resctrl group of the system
	RootClassName = "system/default"
	// RootClassAlias is an alternative name for the root class
	RootClassAlias = ""
)

type control struct {
	grclog.Logger

	resctrlGroupPrefix string
	conf               config
	rawConf            Config
	classes            map[string]*ctrlGroup
}

var log grclog.Logger = grclog.NewLoggerWrapper(stdlog.New(os.Stderr, "[ rdt ] ", 0))

var info *resctrlInfo

var rdt *control

// Function for removing resctrl groups from the filesystem. This is
// configurable because of unit tests.
var groupRemoveFunc func(string) error = os.Remove

// CtrlGroup defines the interface of one goresctrl managed RDT class. It maps
// to one CTRL group directory in the goresctrl pseudo-filesystem.
type CtrlGroup interface {
	ResctrlGroup

	// CreateMonGroup creates a new monitoring group under this CtrlGroup.
	CreateMonGroup(name string, annotations map[string]string) (MonGroup, error)

	// DeleteMonGroup deletes a monitoring group from this CtrlGroup.
	DeleteMonGroup(name string) error

	// DeleteMonGroups deletes all monitoring groups from this CtrlGroup.
	DeleteMonGroups() error

	// GetMonGroup returns a specific monitoring group under this CtrlGroup.
	GetMonGroup(name string) (MonGroup, bool)

	// GetMonGroups returns all monitoring groups under this CtrlGroup.
	GetMonGroups() []MonGroup
}

// ResctrlGroup is the generic interface for resctrl CTRL and MON groups. It
// maps to one CTRL or MON group directory in the goresctrl pseudo-filesystem.
type ResctrlGroup interface {
	// Name returns the name of the group.
	Name() string

	// GetPids returns the process ids assigned to the group.
	GetPids() ([]string, error)

	// AddPids assigns the given process ids to the group.
	AddPids(pids ...string) error

	// GetMonData retrieves the monitoring data of the group.
	GetMonData() MonData
}

// MonGroup represents the interface to a RDT monitoring group. It maps to one
// MON group in the goresctrl filesystem.
type MonGroup interface {
	ResctrlGroup

	// Parent returns the CtrlGroup under which the monitoring group exists.
	Parent() CtrlGroup

	// GetAnnotations returns the annotations stored to the monitoring group.
	GetAnnotations() map[string]string
}

// MonData contains monitoring stats of one monitoring group.
type MonData struct {
	L3 MonL3Data
}

// MonL3Data contains L3 monitoring stats of one monitoring group.
type MonL3Data map[uint64]MonLeafData

// MonLeafData represents the raw numerical stats from one RDT monitor data leaf.
type MonLeafData map[string]uint64

// MonResource is the type of RDT monitoring resource.
type MonResource string

const (
	// MonResourceL3 is the RDT L3 cache monitor resource.
	MonResourceL3 MonResource = "l3"
)

type ctrlGroup struct {
	resctrlGroup

	monPrefix string
	monGroups map[string]*monGroup
}

type monGroup struct {
	resctrlGroup

	annotations map[string]string
}

type resctrlGroup struct {
	prefix string
	name   string
	parent *ctrlGroup // parent for MON groups
}

// SetLogger sets the logger instance to be used by the package. This function
// may be called even before Initialize().
func SetLogger(l grclog.Logger) {
	log = l
	if rdt != nil {
		rdt.setLogger(l)
	}
}

// Initialize detects RDT from the system and initializes control interface of
// the package.
func Initialize(resctrlGroupPrefix string) error {
	var err error

	info = nil
	rdt = nil

	// Get info from the resctrl filesystem
	info, err = getRdtInfo()
	if err != nil {
		return err
	}

	r := &control{Logger: log, resctrlGroupPrefix: resctrlGroupPrefix}

	// NOTE: we lose monitoring group annotations (i.e. prometheus metrics
	// labels) on re-init
	if r.classes, err = r.classesFromResctrlFs(); err != nil {
		return fmt.Errorf("failed to initialize classes from resctrl fs: %v", err)
	}

	rdt = r

	return nil
}

// DiscoverClasses discovers existing classes from the resctrl filesystem.
// Makes it possible to discover gropus with another prefix than was set with
// Initialize(). The original prefix is still used for monitoring groups.
func DiscoverClasses(resctrlGroupPrefix string) error {
	if rdt != nil {
		return rdt.discoverFromResctrl(resctrlGroupPrefix)
	}
	return fmt.Errorf("rdt not initialized")
}

// SetConfig  (re-)configures the resctrl filesystem according to the specified
// configuration.
func SetConfig(c *Config, force bool) error {
	if rdt != nil {
		return rdt.setConfig(c, force)
	}
	return fmt.Errorf("rdt not initialized")
}

// SetConfigFromData takes configuration as raw data, parses it and
// reconfigures the resctrl filesystem.
func SetConfigFromData(data []byte, force bool) error {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse configuration data: %v", err)
	}

	return SetConfig(cfg, force)
}

// SetConfigFromFile reads configuration from the filesystem and reconfigures
// the resctrl filesystem.
func SetConfigFromFile(path string, force bool) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %v", err)
	}

	if err := SetConfigFromData(data, force); err != nil {
		return err
	}

	log.Infof("configuration successfully loaded from %q", path)
	return nil
}

// GetClass returns one RDT class.
func GetClass(name string) (CtrlGroup, bool) {
	if rdt != nil {
		return rdt.getClass(name)
	}
	return nil, false
}

// GetClasses returns all available RDT classes.
func GetClasses() []CtrlGroup {
	if rdt != nil {
		return rdt.getClasses()
	}
	return []CtrlGroup{}
}

// MonSupported returns true if RDT monitoring features are available.
func MonSupported() bool {
	if rdt != nil {
		return rdt.monSupported()
	}
	return false
}

// GetMonFeatures returns the available monitoring stats of each available
// monitoring technology.
func GetMonFeatures() map[MonResource][]string {
	if rdt != nil {
		return rdt.getMonFeatures()
	}
	return map[MonResource][]string{}
}

// IsQualifiedClassName returns true if given string qualifies as a class name
func IsQualifiedClassName(name string) bool {
	// Must be qualified as a file name
	return name == RootClassName || (len(name) < 4096 && name != "." && name != ".." && !strings.ContainsAny(name, "/\n"))
}

func (c *control) getClass(name string) (CtrlGroup, bool) {
	cls, ok := c.classes[unaliasClassName(name)]
	return cls, ok
}

func (c *control) getClasses() []CtrlGroup {
	ret := make([]CtrlGroup, 0, len(c.classes))

	for _, v := range c.classes {
		ret = append(ret, v)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Name() < ret[j].Name() })

	return ret
}

func (c *control) monSupported() bool {
	return info.l3mon.Supported()
}

func (c *control) getMonFeatures() map[MonResource][]string {
	ret := make(map[MonResource][]string)
	if info.l3mon.Supported() {
		ret[MonResourceL3] = append([]string{}, info.l3mon.monFeatures...)
	}

	return ret
}

func (c *control) setLogger(l grclog.Logger) {
	c.Logger = l
}

func (c *control) setConfig(newConfig *Config, force bool) error {
	c.Infof("configuration update")

	conf, err := (*newConfig).resolve()
	if err != nil {
		return fmt.Errorf("invalid configuration: %v", err)
	}

	err = c.configureResctrl(conf, force)
	if err != nil {
		return fmt.Errorf("resctrl configuration failed: %v", err)
	}

	c.conf = conf
	// TODO: we'd better create a deep copy
	c.rawConf = *newConfig
	c.Infof("configuration finished")

	return nil
}

func (c *control) configureResctrl(conf config, force bool) error {
	grclog.DebugBlock(c, "applying resolved config:", "  ", "%s", utils.DumpJSON(conf))

	// Remove stale resctrl groups
	classesFromFs, err := c.classesFromResctrlFs()
	if err != nil {
		return err
	}

	for name, cls := range classesFromFs {
		if _, ok := conf.Classes[cls.name]; !isRootClass(cls.name) && !ok {
			if !force {
				tasks, err := cls.GetPids()
				if err != nil {
					return fmt.Errorf("failed to get resctrl group tasks: %v", err)
				}
				if len(tasks) > 0 {
					return fmt.Errorf("refusing to remove non-empty resctrl group %q", cls.relPath(""))
				}
			}
			log.Debugf("removing existing resctrl group %q", cls.relPath(""))
			err = groupRemoveFunc(cls.path(""))
			if err != nil {
				return fmt.Errorf("failed to remove resctrl group %q: %v", cls.relPath(""), err)
			}

			delete(c.classes, name)
		}
	}

	for name, cls := range c.classes {
		if _, ok := conf.Classes[cls.name]; !ok || cls.prefix != c.resctrlGroupPrefix {
			if !isRootClass(cls.name) {
				log.Debugf("dropping stale class %q (%q)", name, cls.path(""))
				delete(c.classes, name)
			}
		}
	}

	if _, ok := c.classes[RootClassName]; !ok {
		log.Warnf("root class missing from runtime data, re-adding...")
		c.classes[RootClassName] = classesFromFs[RootClassName]
	}

	// Try to apply given configuration
	for name, class := range conf.Classes {
		if _, ok := c.classes[name]; !ok {
			cg, err := newCtrlGroup(c.resctrlGroupPrefix, c.resctrlGroupPrefix, name)
			if err != nil {
				return err
			}
			c.classes[name] = cg
		}
		partition := conf.Partitions[class.Partition]
		if err := c.classes[name].configure(name, class, partition, conf.Options); err != nil {
			return err
		}
	}

	if err := c.pruneMonGroups(); err != nil {
		return err
	}

	return nil
}

func (c *control) discoverFromResctrl(prefix string) error {
	c.Debugf("running class discovery from resctrl filesystem using prefix %q", prefix)

	classesFromFs, err := c.classesFromResctrlFsPrefix(prefix)
	if err != nil {
		return err
	}

	// Drop stale classes
	for name, cls := range c.classes {
		if _, ok := classesFromFs[cls.name]; !ok || cls.prefix != prefix {
			if !isRootClass(cls.name) {
				log.Debugf("dropping stale class %q (%q)", name, cls.path(""))
				delete(c.classes, name)
			}
		}
	}

	for name, cls := range classesFromFs {
		if _, ok := c.classes[name]; !ok {
			c.classes[name] = cls
			log.Debugf("adding discovered class %q (%q)", name, cls.path(""))
		}
	}

	if err := c.pruneMonGroups(); err != nil {
		return err
	}

	return nil
}

func (c *control) classesFromResctrlFs() (map[string]*ctrlGroup, error) {
	return c.classesFromResctrlFsPrefix(c.resctrlGroupPrefix)
}

func (c *control) classesFromResctrlFsPrefix(prefix string) (map[string]*ctrlGroup, error) {
	names := []string{RootClassName}
	if g, err := resctrlGroupsFromFs(prefix, info.resctrlPath); err != nil {
		return nil, err
	} else {
		for _, n := range g {
			if prefix != c.resctrlGroupPrefix &&
				strings.HasPrefix(n, c.resctrlGroupPrefix) &&
				strings.HasPrefix(c.resctrlGroupPrefix, prefix) {
				// Skip groups in the standard namespace
				continue
			}
			names = append(names, n[len(prefix):])
		}
	}

	classes := make(map[string]*ctrlGroup, len(names)+1)
	for _, name := range names {
		g, err := newCtrlGroup(prefix, c.resctrlGroupPrefix, name)
		if err != nil {
			return nil, err
		}
		classes[name] = g
	}

	return classes, nil
}

func (c *control) pruneMonGroups() error {
	for name, cls := range c.classes {
		if err := cls.pruneMonGroups(); err != nil {
			return fmt.Errorf("failed to prune stale monitoring groups of %q: %v", name, err)
		}
	}
	return nil
}

func (c *control) readRdtFile(rdtPath string) ([]byte, error) {
	return ioutil.ReadFile(filepath.Join(info.resctrlPath, rdtPath))
}

func (c *control) writeRdtFile(rdtPath string, data []byte) error {
	if err := ioutil.WriteFile(filepath.Join(info.resctrlPath, rdtPath), data, 0644); err != nil {
		return c.cmdError(err)
	}
	return nil
}

func (c *control) cmdError(origErr error) error {
	errData, readErr := c.readRdtFile(filepath.Join("info", "last_cmd_status"))
	if readErr != nil {
		return origErr
	}
	cmdStatus := strings.TrimSpace(string(errData))
	if len(cmdStatus) > 0 && cmdStatus != "ok" {
		return fmt.Errorf("%s", cmdStatus)
	}
	return origErr
}

func newCtrlGroup(prefix, monPrefix, name string) (*ctrlGroup, error) {
	cg := &ctrlGroup{
		resctrlGroup: resctrlGroup{prefix: prefix, name: name},
		monPrefix:    monPrefix,
	}

	if err := os.Mkdir(cg.path(""), 0755); err != nil && !os.IsExist(err) {
		return nil, err
	}

	var err error
	cg.monGroups, err = cg.monGroupsFromResctrlFs()
	if err != nil {
		return nil, fmt.Errorf("error when retrieving existing monitor groups: %v", err)
	}

	return cg, nil
}

func (c *ctrlGroup) CreateMonGroup(name string, annotations map[string]string) (MonGroup, error) {
	if mg, ok := c.monGroups[name]; ok {
		return mg, nil
	}

	log.Debugf("creating monitoring group %s/%s", c.name, name)
	mg, err := newMonGroup(c.monPrefix, name, c, annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to create new monitoring group %q: %v", name, err)
	}

	c.monGroups[name] = mg

	return mg, err
}

func (c *ctrlGroup) DeleteMonGroup(name string) error {
	mg, ok := c.monGroups[name]
	if !ok {
		log.Warnf("trying to delete non-existent mon group %s/%s", c.name, name)
		return nil
	}

	log.Debugf("deleting monitoring group %s/%s", c.name, name)
	if err := groupRemoveFunc(mg.path("")); err != nil {
		return fmt.Errorf("failed to remove monitoring group %q: %v", mg.relPath(""), err)
	}

	delete(c.monGroups, name)

	return nil
}

func (c *ctrlGroup) DeleteMonGroups() error {
	for name := range c.monGroups {
		if err := c.DeleteMonGroup(name); err != nil {
			return err
		}
	}
	return nil
}

func (c *ctrlGroup) GetMonGroup(name string) (MonGroup, bool) {
	mg, ok := c.monGroups[name]
	return mg, ok
}

func (c *ctrlGroup) GetMonGroups() []MonGroup {
	ret := make([]MonGroup, 0, len(c.monGroups))

	for _, v := range c.monGroups {
		ret = append(ret, v)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Name() < ret[j].Name() })

	return ret
}

func (c *ctrlGroup) configure(name string, class *classConfig,
	partition *partitionConfig, options Options) error {
	schemata := ""

	// Handle cache allocation
	for _, lvl := range []cacheLevel{L2, L3} {
		switch {
		case info.cat[lvl].unified.Supported():
			schema, err := class.CATSchema[lvl].toStr(catSchemaTypeUnified, partition.CAT[lvl])
			if err != nil {
				return err
			}
			schemata += schema
		case info.cat[lvl].data.Supported() || info.cat[lvl].code.Supported():
			schema, err := class.CATSchema[lvl].toStr(catSchemaTypeCode, partition.CAT[lvl])
			if err != nil {
				return err
			}
			schemata += schema

			schema, err = class.CATSchema[lvl].toStr(catSchemaTypeData, partition.CAT[lvl])
			if err != nil {
				return err
			}
			schemata += schema
		default:
			if class.CATSchema[lvl].Alloc != nil && !options.cat(lvl).Optional {
				return fmt.Errorf("%s cache allocation for %q specified in configuration but not supported by system", lvl, name)
			}
		}
	}

	// Handle memory bandwidth allocation
	switch {
	case info.mb.Supported():
		schemata += class.MBSchema.toStr(partition.MB)
	default:
		if class.MBSchema != nil && !options.MB.Optional {
			return fmt.Errorf("memory bandwidth allocation for %q specified in configuration but not supported by system", name)
		}
	}

	if len(schemata) > 0 {
		log.Debugf("writing schemata %q to %q", schemata, c.relPath(""))
		if err := rdt.writeRdtFile(c.relPath("schemata"), []byte(schemata)); err != nil {
			return err
		}
	} else {
		log.Debugf("empty schemata")
	}

	return nil
}

func (c *ctrlGroup) monGroupsFromResctrlFs() (map[string]*monGroup, error) {
	names, err := resctrlGroupsFromFs(c.monPrefix, c.path("mon_groups"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	grps := make(map[string]*monGroup, len(names))
	for _, name := range names {
		name = name[len(c.monPrefix):]
		mg, err := newMonGroup(c.monPrefix, name, c, nil)
		if err != nil {
			return nil, err
		}
		grps[name] = mg
	}
	return grps, nil
}

// Remove empty monitoring groups
func (c *ctrlGroup) pruneMonGroups() error {
	for name, mg := range c.monGroups {
		pids, err := mg.GetPids()
		if err != nil {
			return fmt.Errorf("failed to get pids for monitoring group %q: %v", mg.relPath(""), err)
		}
		if len(pids) == 0 {
			if err := c.DeleteMonGroup(name); err != nil {
				return fmt.Errorf("failed to remove monitoring group %q: %v", mg.relPath(""), err)
			}
		}
	}
	return nil
}

func (r *resctrlGroup) Name() string {
	return r.name
}

func (r *resctrlGroup) GetPids() ([]string, error) {
	data, err := rdt.readRdtFile(r.relPath("tasks"))
	if err != nil {
		return []string{}, err
	}
	split := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(split[0]) > 0 {
		return split, nil
	}
	return []string{}, nil
}

func (r *resctrlGroup) AddPids(pids ...string) error {
	f, err := os.OpenFile(r.path("tasks"), os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, pid := range pids {
		if _, err := f.WriteString(pid + "\n"); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				log.Debugf("no task %s", pid)
			} else {
				return fmt.Errorf("failed to assign processes %v to class %q: %v", pids, r.name, rdt.cmdError(err))
			}
		}
	}
	return nil
}

func (r *resctrlGroup) GetMonData() MonData {
	m := MonData{}

	if info.l3mon.Supported() {
		l3, err := r.getMonL3Data()
		if err != nil {
			log.Warnf("failed to retrieve L3 monitoring data: %v", err)
		} else {
			m.L3 = l3
		}
	}

	return m
}

func (r *resctrlGroup) getMonL3Data() (MonL3Data, error) {
	files, err := ioutil.ReadDir(r.path("mon_data"))
	if err != nil {
		return nil, err
	}

	m := MonL3Data{}
	for _, file := range files {
		name := file.Name()
		if strings.HasPrefix(name, "mon_L3_") {
			// Parse cache id from the dirname
			id, err := strconv.ParseUint(strings.TrimPrefix(name, "mon_L3_"), 10, 32)
			if err != nil {
				// Just print a warning, we try to retrieve as much info as possible
				log.Warnf("error parsing L3 monitor data directory name %q: %v", name, err)
				continue
			}

			data, err := r.getMonLeafData(filepath.Join("mon_data", name))
			if err != nil {
				log.Warnf("failed to read monitor data: %v", err)
				continue
			}

			m[id] = data
		}
	}

	return m, nil
}

func (r *resctrlGroup) getMonLeafData(path string) (MonLeafData, error) {
	files, err := ioutil.ReadDir(r.path(path))
	if err != nil {
		return nil, err
	}

	m := make(MonLeafData, len(files))

	for _, file := range files {
		name := file.Name()

		// We expect that all the files in the dir are regular files
		val, err := readFileUint64(r.path(path, name))
		if err != nil {
			// Just print a warning, we want to retrieve as much info as possible
			log.Warnf("error reading data file: %v", err)
			continue
		}

		m[name] = val
	}
	return m, nil
}

func (r *resctrlGroup) relPath(elem ...string) string {
	if r.parent == nil {
		if r.name == RootClassName {
			return filepath.Join(elem...)
		}
		return filepath.Join(append([]string{r.prefix + r.name}, elem...)...)
	}
	// Parent is only intended for MON groups - non-root CTRL groups are considered
	// as peers to the root CTRL group (as they are in HW) and do not have a parent
	return r.parent.relPath(append([]string{"mon_groups", r.prefix + r.name}, elem...)...)
}

func (r *resctrlGroup) path(elem ...string) string {
	return filepath.Join(info.resctrlPath, r.relPath(elem...))
}

func newMonGroup(prefix string, name string, parent *ctrlGroup, annotations map[string]string) (*monGroup, error) {
	mg := &monGroup{
		resctrlGroup: resctrlGroup{prefix: prefix, name: name, parent: parent},
		annotations:  make(map[string]string, len(annotations))}

	if err := os.Mkdir(mg.path(""), 0755); err != nil && !os.IsExist(err) {
		return nil, err
	}
	for k, v := range annotations {
		mg.annotations[k] = v
	}

	return mg, nil
}

func (m *monGroup) Parent() CtrlGroup {
	return m.parent
}

func (m *monGroup) GetAnnotations() map[string]string {
	a := make(map[string]string, len(m.annotations))
	for k, v := range m.annotations {
		a[k] = v
	}
	return a
}

func resctrlGroupsFromFs(prefix string, path string) ([]string, error) {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}

	grps := make([]string, 0, len(files))
	for _, file := range files {
		filename := file.Name()
		if strings.HasPrefix(filename, prefix) {
			if s, err := os.Stat(filepath.Join(path, filename, "tasks")); err == nil && !s.IsDir() {
				grps = append(grps, filename)
			}
		}
	}
	return grps, nil
}

func isRootClass(name string) bool {
	return name == RootClassName || name == RootClassAlias
}

func unaliasClassName(name string) string {
	if isRootClass(name) {
		return RootClassName
	}
	return name
}
