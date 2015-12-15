// +build libcontainer

package linux

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/docker/containerd/runtime"
	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runc/libcontainer/configs"
	_ "github.com/opencontainers/runc/libcontainer/nsenter"
	"github.com/opencontainers/runc/libcontainer/seccomp"
	"github.com/opencontainers/specs"
)

const (
	RLIMIT_CPU        = iota // CPU time in sec
	RLIMIT_FSIZE             // Maximum filesize
	RLIMIT_DATA              // max data size
	RLIMIT_STACK             // max stack size
	RLIMIT_CORE              // max core file size
	RLIMIT_RSS               // max resident set size
	RLIMIT_NPROC             // max number of processes
	RLIMIT_NOFILE            // max number of open files
	RLIMIT_MEMLOCK           // max locked-in-memory address space
	RLIMIT_AS                // address space limit
	RLIMIT_LOCKS             // maximum file locks held
	RLIMIT_SIGPENDING        // max number of pending signals
	RLIMIT_MSGQUEUE          // maximum bytes in POSIX mqueues
	RLIMIT_NICE              // max nice prio allowed to raise to
	RLIMIT_RTPRIO            // maximum realtime priority
	RLIMIT_RTTIME            // timeout for RT tasks in us
)

var rlimitMap = map[string]int{
	"RLIMIT_CPU":       RLIMIT_CPU,
	"RLIMIT_FSIZE":     RLIMIT_FSIZE,
	"RLIMIT_DATA":      RLIMIT_DATA,
	"RLIMIT_STACK":     RLIMIT_STACK,
	"RLIMIT_CORE":      RLIMIT_CORE,
	"RLIMIT_RSS":       RLIMIT_RSS,
	"RLIMIT_NPROC":     RLIMIT_NPROC,
	"RLIMIT_NOFILE":    RLIMIT_NOFILE,
	"RLIMIT_MEMLOCK":   RLIMIT_MEMLOCK,
	"RLIMIT_AS":        RLIMIT_AS,
	"RLIMIT_LOCKS":     RLIMIT_LOCKS,
	"RLIMIT_SGPENDING": RLIMIT_SIGPENDING,
	"RLIMIT_MSGQUEUE":  RLIMIT_MSGQUEUE,
	"RLIMIT_NICE":      RLIMIT_NICE,
	"RLIMIT_RTPRIO":    RLIMIT_RTPRIO,
	"RLIMIT_RTTIME":    RLIMIT_RTTIME,
}

func strToRlimit(key string) (int, error) {
	rl, ok := rlimitMap[key]
	if !ok {
		return 0, fmt.Errorf("Wrong rlimit value: %s", key)
	}
	return rl, nil
}

const wildcard = -1

var allowedDevices = []*configs.Device{
	// allow mknod for any device
	{
		Type:        'c',
		Major:       wildcard,
		Minor:       wildcard,
		Permissions: "m",
	},
	{
		Type:        'b',
		Major:       wildcard,
		Minor:       wildcard,
		Permissions: "m",
	},
	{
		Path:        "/dev/console",
		Type:        'c',
		Major:       5,
		Minor:       1,
		Permissions: "rwm",
	},
	{
		Path:        "/dev/tty0",
		Type:        'c',
		Major:       4,
		Minor:       0,
		Permissions: "rwm",
	},
	{
		Path:        "/dev/tty1",
		Type:        'c',
		Major:       4,
		Minor:       1,
		Permissions: "rwm",
	},
	// /dev/pts/ - pts namespaces are "coming soon"
	{
		Path:        "",
		Type:        'c',
		Major:       136,
		Minor:       wildcard,
		Permissions: "rwm",
	},
	{
		Path:        "",
		Type:        'c',
		Major:       5,
		Minor:       2,
		Permissions: "rwm",
	},
	// tuntap
	{
		Path:        "",
		Type:        'c',
		Major:       10,
		Minor:       200,
		Permissions: "rwm",
	},
}

var namespaceMapping = map[specs.NamespaceType]configs.NamespaceType{
	specs.PIDNamespace:     configs.NEWPID,
	specs.NetworkNamespace: configs.NEWNET,
	specs.MountNamespace:   configs.NEWNS,
	specs.UserNamespace:    configs.NEWUSER,
	specs.IPCNamespace:     configs.NEWIPC,
	specs.UTSNamespace:     configs.NEWUTS,
}

var mountPropagationMapping = map[string]int{
	"rprivate": syscall.MS_PRIVATE | syscall.MS_REC,
	"private":  syscall.MS_PRIVATE,
	"rslave":   syscall.MS_SLAVE | syscall.MS_REC,
	"slave":    syscall.MS_SLAVE,
	"rshared":  syscall.MS_SHARED | syscall.MS_REC,
	"shared":   syscall.MS_SHARED,
	"":         syscall.MS_PRIVATE | syscall.MS_REC,
}

func init() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		goruntime.GOMAXPROCS(1)
		goruntime.LockOSThread()
		factory, _ := libcontainer.New("")
		if err := factory.StartInitialization(); err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(1)
		}
		panic("--this line should have never been executed, congratulations--")
	}
}

type libcontainerProcess struct {
	process *libcontainer.Process
	spec    specs.Process
}

// change interface to support an error
func (p *libcontainerProcess) Pid() (int, error) {
	pid, err := p.process.Pid()
	if err != nil {
		return -1, err
	}
	return pid, nil
}

func (p *libcontainerProcess) Spec() specs.Process {
	return p.spec
}

func (p *libcontainerProcess) Signal(s os.Signal) error {
	return p.process.Signal(s)
}

func (p *libcontainerProcess) IO() *runtime.IO {
	// TODO(stevvooe): Grab the console here, if possible.

	return &runtime.IO{
		Stdin:  p.process.Stdin,
		Stdout: p.process.Stout,
		Stderr: p.process.Stderr,
	}
}

func (p *libcontainerProcess) Close() error {
	// in close we always need to call wait to close/flush any pipes
	_, err := p.process.Wait()
	// explicitly close any open fd on the process
	for _, cl := range []interface{}{
		p.process.Stderr,
		p.process.Stdout,
		p.process.Stdin,
	} {
		if cl != nil {
			if c, ok := cl.(io.Closer); ok {
				c.Close()
			}
		}
	}
	return err
}

type libcontainerContainer struct {
	c                   libcontainer.Container
	initProcess         *libcontainerProcess
	additionalProcesses map[int]*libcontainerProcess
	exitStatus          int
	exited              bool
	path                string
}

func (c *libcontainerContainer) Checkpoints() ([]runtime.Checkpoint, error) {
	out := []runtime.Checkpoint{}
	files, err := ioutil.ReadDir(c.getCheckpointPath(""))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, fi := range files {
		out = append(out, runtime.Checkpoint{
			Name:      fi.Name(),
			Timestamp: fi.ModTime(),
		})
	}
	return out, nil
}

func (c *libcontainerContainer) DeleteCheckpoint(name string) error {
	path := c.getCheckpointPath(name)
	if err := os.RemoveAll(path); err != nil {
		if os.IsNotExist(err) {
			return runtime.ErrCheckpointNotExists
		}
		return err
	}
	return nil
}

func (c *libcontainerContainer) getCheckpointPath(name string) string {
	return filepath.Join(c.path, "checkpoints", name)
}

func (c *libcontainerContainer) Checkpoint(cp runtime.Checkpoint) error {
	opts := c.createCheckpointOpts(cp)
	if err := os.MkdirAll(filepath.Dir(opts.ImagesDirectory), 0755); err != nil {
		return err
	}
	// mkdir is atomic so if it already exists we can fail
	if err := os.Mkdir(opts.ImagesDirectory, 0755); err != nil {
		if os.IsExist(err) {
			return runtime.ErrCheckpointExists
		}
		return err
	}
	if err := c.c.Checkpoint(opts); err != nil {
		return err
	}
	return nil
}

func (c *libcontainerContainer) createCheckpointOpts(cp runtime.Checkpoint) *libcontainer.CriuOpts {
	opts := libcontainer.CriuOpts{}
	opts.LeaveRunning = !cp.Exit
	opts.ShellJob = cp.Shell
	opts.TcpEstablished = cp.Tcp
	opts.ExternalUnixConnections = cp.UnixSockets
	opts.ImagesDirectory = c.getCheckpointPath(cp.Name)
	return &opts
}

func (c *libcontainerContainer) Restore(name string) error {
	path := c.getCheckpointPath(name)
	var opts libcontainer.CriuOpts
	opts.ImagesDirectory = path
	return c.c.Restore(c.initProcess.process, &opts)
}

func (c *libcontainerContainer) Resume() error {
	return c.c.Resume()
}

func (c *libcontainerContainer) Pause() error {
	return c.c.Pause()
}

func (c *libcontainerContainer) State() runtime.State {
	s := runtime.State{}
	// TODO: what to do with error
	state, err := c.c.Status()
	if err != nil {
		return s
	}
	switch state {
	case libcontainer.Paused, libcontainer.Pausing:
		s.Status = runtime.Paused
	default:
		s.Status = runtime.Running
	}
	return s
}

func (c *libcontainerContainer) ID() string {
	return c.c.ID()
}

func (c *libcontainerContainer) Path() string {
	return c.path
}

func (c *libcontainerContainer) Pid() (int, error) {
	return c.initProcess.Pid()
}

func (c *libcontainerContainer) Start() error {
	return c.c.Start(c.initProcess.process)
}

func (c *libcontainerContainer) SetExited(status int) {
	c.exitStatus = status
	// meh
	c.exited = true
	c.initProcess.Close()
}

func (c *libcontainerContainer) Stats() (*runtime.Stat, error) {
	now := time.Now()
	stats, err := c.c.Stats()
	if err != nil {
		return nil, err
	}
	return &runtime.Stat{
		Timestamp: now,
		Data:      stats,
	}, nil
}

func (c *libcontainerContainer) Delete() error {
	return c.c.Destroy()
}

func (c *libcontainerContainer) Processes() ([]runtime.Process, error) {
	procs := []runtime.Process{
		c.initProcess,
	}
	for _, p := range c.additionalProcesses {
		procs = append(procs, p)
	}
	return procs, nil
}

func (c *libcontainerContainer) RemoveProcess(pid int) error {
	proc, ok := c.additionalProcesses[pid]
	if !ok {
		return runtime.ErrNotChildProcess
	}
	err := proc.Close()
	delete(c.additionalProcesses, pid)
	return err
}

func NewRuntime(stateDir string) (runtime.Runtime, error) {
	f, err := libcontainer.New(stateDir, libcontainer.Cgroupfs, func(l *libcontainer.LinuxFactory) error {
		//l.CriuPath = context.GlobalString("criu")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &libcontainerRuntime{
		factory: f,
	}, nil
}

type libcontainerRuntime struct {
	factory libcontainer.Factory
}

func (r *libcontainerRuntime) Type() string {
	return "libcontainer"
}

func (r *libcontainerRuntime) Create(id, bundlePath, consolePath string) (runtime.Container, *runtime.IO, error) {
	spec, rspec, err := r.loadSpec(
		filepath.Join(bundlePath, "config.json"),
		filepath.Join(bundlePath, "runtime.json"),
	)
	if err != nil {
		return nil, nil, err
	}
	config, err := r.createLibcontainerConfig(id, bundlePath, spec, rspec)
	if err != nil {
		return nil, nil, err
	}
	container, err := r.factory.Create(id, config)
	if err != nil {
		return nil, nil, fmt.Errorf("create container: %v", err)
	}
	process, err := r.newProcess(spec.Process)
	if err != nil {
		return nil, nil, err
	}

	var rio runtime.IO
	if spec.Process.Terminal {
		if err := process.ConsoleFromPath(consolePath); err != nil {
			return nil, nil, err
		}
	} else {
		i, err := process.InitializeIO(int(spec.Process.User.UID))
		if err != nil {
			return nil, nil, err
		}

		// NOTE(stevvooe): Do these need to be set?
		rio.Stdin = i.Stdin
		rio.Stderr = i.Stderr
		rio.Stdout = i.Stdout
	}

	c := &libcontainerContainer{
		c:                   container,
		additionalProcesses: make(map[int]*libcontainerProcess),
		initProcess: &libcontainerProcess{
			process: process,
			spec:    spec.Process,
		},
		path: bundlePath,
	}
	return c, &rio, nil
}

func (r *libcontainerRuntime) StartProcess(ci runtime.Container, p specs.Process) (runtime.Process, *runtime.IO, error) {
	c, ok := ci.(*libcontainerContainer)
	if !ok {
		return nil, nil, runtime.ErrInvalidContainerType
	}
	process, err := r.newProcess(p)
	if err != nil {
		return nil, nil, err
	}
	i, err := process.InitializeIO(int(p.User.UID))
	if err != nil {
		return nil, nil, err
	}
	if err := c.c.Start(process); err != nil {
		return nil, nil, err
	}
	lp := &libcontainerProcess{
		process: process,
		spec:    p,
	}
	pid, err := process.Pid()
	if err != nil {
		return nil, nil, err
	}
	c.additionalProcesses[pid] = lp
	return lp, &runtime.IO{
		Stdin:  i.Stdin,
		Stdout: i.Stdout,
		Stderr: i.Stderr,
	}, nil
}

// newProcess returns a new libcontainer Process with the arguments from the
// spec and stdio from the current process.
func (r *libcontainerRuntime) newProcess(p specs.Process) (*libcontainer.Process, error) {
	return &libcontainer.Process{
		Args: p.Args,
		Env:  p.Env,
		// TODO: fix libcontainer's API to better support uid/gid in a typesafe way.
		User: fmt.Sprintf("%d:%d", p.User.UID, p.User.GID),
		Cwd:  p.Cwd,
	}, nil
}

// loadSpec loads the specification from the provided path.
// If the path is empty then the default path will be "config.json"
func (r *libcontainerRuntime) loadSpec(cPath, rPath string) (spec *specs.LinuxSpec, rspec *specs.LinuxRuntimeSpec, err error) {
	cf, err := os.Open(cPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("JSON specification file at %s not found", cPath)
		}
		return spec, rspec, err
	}
	defer cf.Close()

	rf, err := os.Open(rPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("JSON runtime config file at %s not found", rPath)
		}
		return spec, rspec, err
	}
	defer rf.Close()

	if err = json.NewDecoder(cf).Decode(&spec); err != nil {
		return spec, rspec, fmt.Errorf("unmarshal %s: %v", cPath, err)
	}
	if err = json.NewDecoder(rf).Decode(&rspec); err != nil {
		return spec, rspec, fmt.Errorf("unmarshal %s: %v", rPath, err)
	}
	return spec, rspec, r.checkSpecVersion(spec)
}

// checkSpecVersion makes sure that the spec version matches runc's while we are in the initial
// development period.  It is better to hard fail than have missing fields or options in the spec.
func (r *libcontainerRuntime) checkSpecVersion(s *specs.LinuxSpec) error {
	if s.Version != specs.Version {
		return fmt.Errorf("spec version is not compatible with implemented version %q: spec %q", specs.Version, s.Version)
	}
	return nil
}

func (r *libcontainerRuntime) createLibcontainerConfig(cgroupName, bundlePath string, spec *specs.LinuxSpec, rspec *specs.LinuxRuntimeSpec) (*configs.Config, error) {
	rootfsPath := spec.Root.Path
	if !filepath.IsAbs(rootfsPath) {
		rootfsPath = filepath.Join(bundlePath, rootfsPath)
	}
	config := &configs.Config{
		Rootfs:       rootfsPath,
		Capabilities: spec.Linux.Capabilities,
		Readonlyfs:   spec.Root.Readonly,
		Hostname:     spec.Hostname,
	}
	for _, ns := range rspec.Linux.Namespaces {
		t, exists := namespaceMapping[ns.Type]
		if !exists {
			return nil, fmt.Errorf("namespace %q does not exist", ns)
		}
		config.Namespaces.Add(t, ns.Path)
	}
	if config.Namespaces.Contains(configs.NEWNET) {
		config.Networks = []*configs.Network{
			{
				Type: "loopback",
			},
		}
	}
	for _, mp := range spec.Mounts {
		m, ok := rspec.Mounts[mp.Name]
		if !ok {
			return nil, fmt.Errorf("Mount with Name %q not found in runtime config", mp.Name)
		}
		config.Mounts = append(config.Mounts, r.createLibcontainerMount(bundlePath, mp.Path, m))
	}
	if err := r.createDevices(rspec, config); err != nil {
		return nil, err
	}
	if err := r.setupUserNamespace(rspec, config); err != nil {
		return nil, err
	}
	for _, rlimit := range rspec.Linux.Rlimits {
		rl, err := r.createLibContainerRlimit(rlimit)
		if err != nil {
			return nil, err
		}
		config.Rlimits = append(config.Rlimits, rl)
	}
	c, err := r.createCgroupConfig(cgroupName, rspec, config.Devices)
	if err != nil {
		return nil, err
	}
	config.Cgroups = c
	if config.Readonlyfs {
		r.setReadonly(config)
		config.MaskPaths = []string{
			"/proc/kcore",
		}
		config.ReadonlyPaths = []string{
			"/proc/sys", "/proc/sysrq-trigger", "/proc/irq", "/proc/bus",
		}
	}
	seccomp, err := r.setupSeccomp(&rspec.Linux.Seccomp)
	if err != nil {
		return nil, err
	}
	config.Seccomp = seccomp
	config.Sysctl = rspec.Linux.Sysctl
	config.ProcessLabel = rspec.Linux.SelinuxProcessLabel
	config.AppArmorProfile = rspec.Linux.ApparmorProfile
	for _, g := range spec.Process.User.AdditionalGids {
		config.AdditionalGroups = append(config.AdditionalGroups, strconv.FormatUint(uint64(g), 10))
	}
	r.createHooks(rspec, config)
	config.Version = specs.Version
	return config, nil
}

func (r *libcontainerRuntime) createLibcontainerMount(cwd, dest string, m specs.Mount) *configs.Mount {
	flags, pgflags, data := parseMountOptions(m.Options)
	source := m.Source
	if m.Type == "bind" {
		if !filepath.IsAbs(source) {
			source = filepath.Join(cwd, m.Source)
		}
	}
	return &configs.Mount{
		Device:           m.Type,
		Source:           source,
		Destination:      dest,
		Data:             data,
		Flags:            flags,
		PropagationFlags: pgflags,
	}
}

func (rt *libcontainerRuntime) createCgroupConfig(name string, spec *specs.LinuxRuntimeSpec, devices []*configs.Device) (*configs.Cgroup, error) {
	c := &configs.Cgroup{
		Name:           name,
		Parent:         "/containerd",
		AllowedDevices: append(devices, allowedDevices...),
	}
	r := spec.Linux.Resources
	c.Memory = int64(r.Memory.Limit)
	c.MemoryReservation = int64(r.Memory.Reservation)
	c.MemorySwap = int64(r.Memory.Swap)
	c.KernelMemory = int64(r.Memory.Kernel)
	c.MemorySwappiness = int64(r.Memory.Swappiness)
	c.CpuShares = int64(r.CPU.Shares)
	c.CpuQuota = int64(r.CPU.Quota)
	c.CpuPeriod = int64(r.CPU.Period)
	c.CpuRtRuntime = int64(r.CPU.RealtimeRuntime)
	c.CpuRtPeriod = int64(r.CPU.RealtimePeriod)
	c.CpusetCpus = r.CPU.Cpus
	c.CpusetMems = r.CPU.Mems
	c.BlkioWeight = r.BlockIO.Weight
	c.BlkioLeafWeight = r.BlockIO.LeafWeight
	for _, wd := range r.BlockIO.WeightDevice {
		weightDevice := configs.NewWeightDevice(wd.Major, wd.Minor, wd.Weight, wd.LeafWeight)
		c.BlkioWeightDevice = append(c.BlkioWeightDevice, weightDevice)
	}
	for _, td := range r.BlockIO.ThrottleReadBpsDevice {
		throttleDevice := configs.NewThrottleDevice(td.Major, td.Minor, td.Rate)
		c.BlkioThrottleReadBpsDevice = append(c.BlkioThrottleReadBpsDevice, throttleDevice)
	}
	for _, td := range r.BlockIO.ThrottleWriteBpsDevice {
		throttleDevice := configs.NewThrottleDevice(td.Major, td.Minor, td.Rate)
		c.BlkioThrottleWriteBpsDevice = append(c.BlkioThrottleWriteBpsDevice, throttleDevice)
	}
	for _, td := range r.BlockIO.ThrottleReadIOPSDevice {
		throttleDevice := configs.NewThrottleDevice(td.Major, td.Minor, td.Rate)
		c.BlkioThrottleReadIOPSDevice = append(c.BlkioThrottleReadIOPSDevice, throttleDevice)
	}
	for _, td := range r.BlockIO.ThrottleWriteIOPSDevice {
		throttleDevice := configs.NewThrottleDevice(td.Major, td.Minor, td.Rate)
		c.BlkioThrottleWriteIOPSDevice = append(c.BlkioThrottleWriteIOPSDevice, throttleDevice)
	}
	for _, l := range r.HugepageLimits {
		c.HugetlbLimit = append(c.HugetlbLimit, &configs.HugepageLimit{
			Pagesize: l.Pagesize,
			Limit:    l.Limit,
		})
	}
	c.OomKillDisable = r.DisableOOMKiller
	c.NetClsClassid = r.Network.ClassID
	for _, m := range r.Network.Priorities {
		c.NetPrioIfpriomap = append(c.NetPrioIfpriomap, &configs.IfPrioMap{
			Interface: m.Name,
			Priority:  int64(m.Priority),
		})
	}
	return c, nil
}

func (r *libcontainerRuntime) createDevices(spec *specs.LinuxRuntimeSpec, config *configs.Config) error {
	for _, d := range spec.Linux.Devices {
		device := &configs.Device{
			Type:        d.Type,
			Path:        d.Path,
			Major:       d.Major,
			Minor:       d.Minor,
			Permissions: d.Permissions,
			FileMode:    d.FileMode,
			Uid:         d.UID,
			Gid:         d.GID,
		}
		config.Devices = append(config.Devices, device)
	}
	return nil
}

func (r *libcontainerRuntime) setReadonly(config *configs.Config) {
	for _, m := range config.Mounts {
		if m.Device == "sysfs" {
			m.Flags |= syscall.MS_RDONLY
		}
	}
}

func (r *libcontainerRuntime) setupUserNamespace(spec *specs.LinuxRuntimeSpec, config *configs.Config) error {
	if len(spec.Linux.UIDMappings) == 0 {
		return nil
	}
	config.Namespaces.Add(configs.NEWUSER, "")
	create := func(m specs.IDMapping) configs.IDMap {
		return configs.IDMap{
			HostID:      int(m.HostID),
			ContainerID: int(m.ContainerID),
			Size:        int(m.Size),
		}
	}
	for _, m := range spec.Linux.UIDMappings {
		config.UidMappings = append(config.UidMappings, create(m))
	}
	for _, m := range spec.Linux.GIDMappings {
		config.GidMappings = append(config.GidMappings, create(m))
	}
	rootUID, err := config.HostUID()
	if err != nil {
		return err
	}
	rootGID, err := config.HostGID()
	if err != nil {
		return err
	}
	for _, node := range config.Devices {
		node.Uid = uint32(rootUID)
		node.Gid = uint32(rootGID)
	}
	return nil
}

func (r *libcontainerRuntime) createLibContainerRlimit(rlimit specs.Rlimit) (configs.Rlimit, error) {
	rl, err := strToRlimit(rlimit.Type)
	if err != nil {
		return configs.Rlimit{}, err
	}
	return configs.Rlimit{
		Type: rl,
		Hard: uint64(rlimit.Hard),
		Soft: uint64(rlimit.Soft),
	}, nil
}

// parseMountOptions parses the string and returns the flags, propagation
// flags and any mount data that it contains.
func parseMountOptions(options []string) (int, []int, string) {
	var (
		flag   int
		pgflag []int
		data   []string
	)
	flags := map[string]struct {
		clear bool
		flag  int
	}{
		"async":         {true, syscall.MS_SYNCHRONOUS},
		"atime":         {true, syscall.MS_NOATIME},
		"bind":          {false, syscall.MS_BIND},
		"defaults":      {false, 0},
		"dev":           {true, syscall.MS_NODEV},
		"diratime":      {true, syscall.MS_NODIRATIME},
		"dirsync":       {false, syscall.MS_DIRSYNC},
		"exec":          {true, syscall.MS_NOEXEC},
		"mand":          {false, syscall.MS_MANDLOCK},
		"noatime":       {false, syscall.MS_NOATIME},
		"nodev":         {false, syscall.MS_NODEV},
		"nodiratime":    {false, syscall.MS_NODIRATIME},
		"noexec":        {false, syscall.MS_NOEXEC},
		"nomand":        {true, syscall.MS_MANDLOCK},
		"norelatime":    {true, syscall.MS_RELATIME},
		"nostrictatime": {true, syscall.MS_STRICTATIME},
		"nosuid":        {false, syscall.MS_NOSUID},
		"rbind":         {false, syscall.MS_BIND | syscall.MS_REC},
		"relatime":      {false, syscall.MS_RELATIME},
		"remount":       {false, syscall.MS_REMOUNT},
		"ro":            {false, syscall.MS_RDONLY},
		"rw":            {true, syscall.MS_RDONLY},
		"strictatime":   {false, syscall.MS_STRICTATIME},
		"suid":          {true, syscall.MS_NOSUID},
		"sync":          {false, syscall.MS_SYNCHRONOUS},
	}
	propagationFlags := map[string]struct {
		clear bool
		flag  int
	}{
		"private":     {false, syscall.MS_PRIVATE},
		"shared":      {false, syscall.MS_SHARED},
		"slave":       {false, syscall.MS_SLAVE},
		"unbindable":  {false, syscall.MS_UNBINDABLE},
		"rprivate":    {false, syscall.MS_PRIVATE | syscall.MS_REC},
		"rshared":     {false, syscall.MS_SHARED | syscall.MS_REC},
		"rslave":      {false, syscall.MS_SLAVE | syscall.MS_REC},
		"runbindable": {false, syscall.MS_UNBINDABLE | syscall.MS_REC},
	}
	for _, o := range options {
		// If the option does not exist in the flags table or the flag
		// is not supported on the platform,
		// then it is a data value for a specific fs type
		if f, exists := flags[o]; exists && f.flag != 0 {
			if f.clear {
				flag &= ^f.flag
			} else {
				flag |= f.flag
			}
		} else if f, exists := propagationFlags[o]; exists && f.flag != 0 {
			pgflag = append(pgflag, f.flag)
		} else {
			data = append(data, o)
		}
	}
	return flag, pgflag, strings.Join(data, ",")
}

func (r *libcontainerRuntime) setupSeccomp(config *specs.Seccomp) (*configs.Seccomp, error) {
	if config == nil {
		return nil, nil
	}

	// No default action specified, no syscalls listed, assume seccomp disabled
	if config.DefaultAction == "" && len(config.Syscalls) == 0 {
		return nil, nil
	}

	newConfig := new(configs.Seccomp)
	newConfig.Syscalls = []*configs.Syscall{}

	if len(config.Architectures) > 0 {
		newConfig.Architectures = []string{}
		for _, arch := range config.Architectures {
			newArch, err := seccomp.ConvertStringToArch(string(arch))
			if err != nil {
				return nil, err
			}
			newConfig.Architectures = append(newConfig.Architectures, newArch)
		}
	}

	// Convert default action from string representation
	newDefaultAction, err := seccomp.ConvertStringToAction(string(config.DefaultAction))
	if err != nil {
		return nil, err
	}
	newConfig.DefaultAction = newDefaultAction

	// Loop through all syscall blocks and convert them to libcontainer format
	for _, call := range config.Syscalls {
		newAction, err := seccomp.ConvertStringToAction(string(call.Action))
		if err != nil {
			return nil, err
		}

		newCall := configs.Syscall{
			Name:   call.Name,
			Action: newAction,
			Args:   []*configs.Arg{},
		}

		// Loop through all the arguments of the syscall and convert them
		for _, arg := range call.Args {
			newOp, err := seccomp.ConvertStringToOperator(string(arg.Op))
			if err != nil {
				return nil, err
			}

			newArg := configs.Arg{
				Index:    arg.Index,
				Value:    arg.Value,
				ValueTwo: arg.ValueTwo,
				Op:       newOp,
			}

			newCall.Args = append(newCall.Args, &newArg)
		}

		newConfig.Syscalls = append(newConfig.Syscalls, &newCall)
	}

	return newConfig, nil
}

func (r *libcontainerRuntime) createHooks(rspec *specs.LinuxRuntimeSpec, config *configs.Config) {
	config.Hooks = &configs.Hooks{}
	for _, h := range rspec.Hooks.Prestart {
		cmd := configs.Command{
			Path: h.Path,
			Args: h.Args,
			Env:  h.Env,
		}
		config.Hooks.Prestart = append(config.Hooks.Prestart, configs.NewCommandHook(cmd))
	}
	for _, h := range rspec.Hooks.Poststop {
		cmd := configs.Command{
			Path: h.Path,
			Args: h.Args,
			Env:  h.Env,
		}
		config.Hooks.Poststop = append(config.Hooks.Poststop, configs.NewCommandHook(cmd))
	}
}
