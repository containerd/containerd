package validate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blang/semver"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/syndtr/gocapability/capability"
)

const specConfig = "config.json"

var (
	defaultRlimits = []string{
		"RLIMIT_AS",
		"RLIMIT_CORE",
		"RLIMIT_CPU",
		"RLIMIT_DATA",
		"RLIMIT_FSIZE",
		"RLIMIT_LOCKS",
		"RLIMIT_MEMLOCK",
		"RLIMIT_MSGQUEUE",
		"RLIMIT_NICE",
		"RLIMIT_NOFILE",
		"RLIMIT_NPROC",
		"RLIMIT_RSS",
		"RLIMIT_RTPRIO",
		"RLIMIT_RTTIME",
		"RLIMIT_SIGPENDING",
		"RLIMIT_STACK",
	}
)

// Validator represents a validator for runtime bundle
type Validator struct {
	spec         *rspec.Spec
	bundlePath   string
	HostSpecific bool
	platform     string
}

// NewValidator creates a Validator
func NewValidator(spec *rspec.Spec, bundlePath string, hostSpecific bool, platform string) Validator {
	if hostSpecific && platform != runtime.GOOS {
		platform = runtime.GOOS
	}
	return Validator{
		spec:         spec,
		bundlePath:   bundlePath,
		HostSpecific: hostSpecific,
		platform:     platform,
	}
}

// NewValidatorFromPath creates a Validator with specified bundle path
func NewValidatorFromPath(bundlePath string, hostSpecific bool, platform string) (Validator, error) {
	if hostSpecific && platform != runtime.GOOS {
		platform = runtime.GOOS
	}
	if bundlePath == "" {
		return Validator{}, fmt.Errorf("Bundle path shouldn't be empty")
	}

	if _, err := os.Stat(bundlePath); err != nil {
		return Validator{}, err
	}

	configPath := filepath.Join(bundlePath, specConfig)
	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		return Validator{}, err
	}
	if !utf8.Valid(content) {
		return Validator{}, fmt.Errorf("%q is not encoded in UTF-8", configPath)
	}
	var spec rspec.Spec
	if err = json.Unmarshal(content, &spec); err != nil {
		return Validator{}, err
	}

	return NewValidator(&spec, bundlePath, hostSpecific, platform), nil
}

// CheckAll checks all parts of runtime bundle
func (v *Validator) CheckAll() (msgs []string) {
	msgs = append(msgs, v.CheckPlatform()...)
	msgs = append(msgs, v.CheckRootfsPath()...)
	msgs = append(msgs, v.CheckMandatoryFields()...)
	msgs = append(msgs, v.CheckSemVer()...)
	msgs = append(msgs, v.CheckMounts()...)
	msgs = append(msgs, v.CheckProcess()...)
	msgs = append(msgs, v.CheckHooks()...)
	if v.spec.Linux != nil {
		msgs = append(msgs, v.CheckLinux()...)
	}

	return
}

// CheckRootfsPath checks status of v.spec.Root.Path
func (v *Validator) CheckRootfsPath() (msgs []string) {
	logrus.Debugf("check rootfs path")

	absBundlePath, err := filepath.Abs(v.bundlePath)
	if err != nil {
		msgs = append(msgs, fmt.Sprintf("unable to convert %q to an absolute path", v.bundlePath))
	}

	var rootfsPath string
	var absRootPath string
	if filepath.IsAbs(v.spec.Root.Path) {
		rootfsPath = v.spec.Root.Path
		absRootPath = filepath.Clean(rootfsPath)
	} else {
		var err error
		rootfsPath = filepath.Join(v.bundlePath, v.spec.Root.Path)
		absRootPath, err = filepath.Abs(rootfsPath)
		if err != nil {
			msgs = append(msgs, fmt.Sprintf("unable to convert %q to an absolute path", rootfsPath))
		}
	}

	if fi, err := os.Stat(rootfsPath); err != nil {
		msgs = append(msgs, fmt.Sprintf("Cannot find the root path %q", rootfsPath))
	} else if !fi.IsDir() {
		msgs = append(msgs, fmt.Sprintf("The root path %q is not a directory.", rootfsPath))
	}

	rootParent := filepath.Dir(absRootPath)
	if absRootPath == string(filepath.Separator) || rootParent != absBundlePath {
		msgs = append(msgs, fmt.Sprintf("root.path is %q, but it MUST be a child of %q", v.spec.Root.Path, absBundlePath))
	}

	if v.platform == "windows" {
		if v.spec.Root.Readonly {
			msgs = append(msgs, "root.readonly field MUST be omitted or false when target platform is windows")
		}
	}

	return
}

// CheckSemVer checks v.spec.Version
func (v *Validator) CheckSemVer() (msgs []string) {
	logrus.Debugf("check semver")

	version := v.spec.Version
	_, err := semver.Parse(version)
	if err != nil {
		msgs = append(msgs, fmt.Sprintf("%q is not valid SemVer: %s", version, err.Error()))
	}
	if version != rspec.Version {
		msgs = append(msgs, fmt.Sprintf("internal error: validate currently only handles version %s, but the supplied configuration targets %s", rspec.Version, version))
	}

	return
}

// CheckHooks check v.spec.Hooks
func (v *Validator) CheckHooks() (msgs []string) {
	logrus.Debugf("check hooks")

	if v.spec.Hooks != nil {
		msgs = append(msgs, checkEventHooks("pre-start", v.spec.Hooks.Prestart, v.HostSpecific)...)
		msgs = append(msgs, checkEventHooks("post-start", v.spec.Hooks.Poststart, v.HostSpecific)...)
		msgs = append(msgs, checkEventHooks("post-stop", v.spec.Hooks.Poststop, v.HostSpecific)...)
	}

	return
}

func checkEventHooks(hookType string, hooks []rspec.Hook, hostSpecific bool) (msgs []string) {
	for _, hook := range hooks {
		if !filepath.IsAbs(hook.Path) {
			msgs = append(msgs, fmt.Sprintf("The %s hook %v: is not absolute path", hookType, hook.Path))
		}

		if hostSpecific {
			fi, err := os.Stat(hook.Path)
			if err != nil {
				msgs = append(msgs, fmt.Sprintf("Cannot find %s hook: %v", hookType, hook.Path))
			}
			if fi.Mode()&0111 == 0 {
				msgs = append(msgs, fmt.Sprintf("The %s hook %v: is not executable", hookType, hook.Path))
			}
		}

		for _, env := range hook.Env {
			if !envValid(env) {
				msgs = append(msgs, fmt.Sprintf("Env %q for hook %v is in the invalid form.", env, hook.Path))
			}
		}
	}

	return
}

// CheckProcess checks v.spec.Process
func (v *Validator) CheckProcess() (msgs []string) {
	logrus.Debugf("check process")

	process := v.spec.Process
	if !filepath.IsAbs(process.Cwd) {
		msgs = append(msgs, fmt.Sprintf("cwd %q is not an absolute path", process.Cwd))
	}

	for _, env := range process.Env {
		if !envValid(env) {
			msgs = append(msgs, fmt.Sprintf("env %q should be in the form of 'key=value'. The left hand side must consist solely of letters, digits, and underscores '_'.", env))
		}
	}

	if len(process.Args) == 0 {
		msgs = append(msgs, fmt.Sprintf("args must not be empty"))
	} else {
		if filepath.IsAbs(process.Args[0]) {
			var rootfsPath string
			if filepath.IsAbs(v.spec.Root.Path) {
				rootfsPath = v.spec.Root.Path
			} else {
				rootfsPath = filepath.Join(v.bundlePath, v.spec.Root.Path)
			}
			absPath := filepath.Join(rootfsPath, process.Args[0])
			fileinfo, err := os.Stat(absPath)
			if os.IsNotExist(err) {
				logrus.Warnf("executable %q is not available in rootfs currently", process.Args[0])
			} else if err != nil {
				msgs = append(msgs, err.Error())
			} else {
				m := fileinfo.Mode()
				if m.IsDir() || m&0111 == 0 {
					msgs = append(msgs, fmt.Sprintf("arg %q is not executable", process.Args[0]))
				}
			}
		}
	}

	if v.spec.Process.Capabilities != nil {
		msgs = append(msgs, v.CheckCapabilities()...)
	}
	msgs = append(msgs, v.CheckRlimits()...)

	if v.platform == "linux" {
		if len(process.ApparmorProfile) > 0 {
			profilePath := filepath.Join(v.bundlePath, v.spec.Root.Path, "/etc/apparmor.d", process.ApparmorProfile)
			_, err := os.Stat(profilePath)
			if err != nil {
				msgs = append(msgs, err.Error())
			}
		}
	}

	return
}

// CheckCapabilities checks v.spec.Process.Capabilities
func (v *Validator) CheckCapabilities() (msgs []string) {
	process := v.spec.Process
	if v.platform == "linux" {
		var effective, permitted, inheritable, ambient bool
		caps := make(map[string][]string)

		for _, cap := range process.Capabilities.Bounding {
			caps[cap] = append(caps[cap], "bounding")
		}
		for _, cap := range process.Capabilities.Effective {
			caps[cap] = append(caps[cap], "effective")
		}
		for _, cap := range process.Capabilities.Inheritable {
			caps[cap] = append(caps[cap], "inheritable")
		}
		for _, cap := range process.Capabilities.Permitted {
			caps[cap] = append(caps[cap], "permitted")
		}
		for _, cap := range process.Capabilities.Ambient {
			caps[cap] = append(caps[cap], "ambient")
		}

		for capability, owns := range caps {
			if err := CapValid(capability, v.HostSpecific); err != nil {
				msgs = append(msgs, fmt.Sprintf("capability %q is not valid, man capabilities(7)", capability))
			}

			effective, permitted, ambient, inheritable = false, false, false, false
			for _, set := range owns {
				if set == "effective" {
					effective = true
				}
				if set == "inheritable" {
					inheritable = true
				}
				if set == "permitted" {
					permitted = true
				}
				if set == "ambient" {
					ambient = true
				}
			}
			if effective && !permitted {
				msgs = append(msgs, fmt.Sprintf("effective capability %q is not allowed, as it's not permitted", capability))
			}
			if ambient && !(effective && inheritable) {
				msgs = append(msgs, fmt.Sprintf("ambient capability %q is not allowed, as it's not permitted and inheribate", capability))
			}
		}
	} else {
		logrus.Warnf("process.capabilities validation not yet implemented for OS %q", v.platform)
	}

	return
}

// CheckRlimits checks v.spec.Process.Rlimits
func (v *Validator) CheckRlimits() (msgs []string) {
	process := v.spec.Process
	for index, rlimit := range process.Rlimits {
		for i := index + 1; i < len(process.Rlimits); i++ {
			if process.Rlimits[index].Type == process.Rlimits[i].Type {
				msgs = append(msgs, fmt.Sprintf("rlimit can not contain the same type %q.", process.Rlimits[index].Type))
			}
		}
		msgs = append(msgs, v.rlimitValid(rlimit)...)
	}

	return
}

func supportedMountTypes(OS string, hostSpecific bool) (map[string]bool, error) {
	supportedTypes := make(map[string]bool)

	if OS != "linux" && OS != "windows" {
		logrus.Warnf("%v is not supported to check mount type", OS)
		return nil, nil
	} else if OS == "windows" {
		supportedTypes["ntfs"] = true
		return supportedTypes, nil
	}

	if hostSpecific {
		f, err := os.Open("/proc/filesystems")
		if err != nil {
			return nil, err
		}
		defer f.Close()

		s := bufio.NewScanner(f)
		for s.Scan() {
			if err := s.Err(); err != nil {
				return supportedTypes, err
			}

			text := s.Text()
			parts := strings.Split(text, "\t")
			if len(parts) > 1 {
				supportedTypes[parts[1]] = true
			} else {
				supportedTypes[parts[0]] = true
			}
		}

		supportedTypes["bind"] = true

		return supportedTypes, nil
	}
	logrus.Warn("Checking linux mount types without --host-specific is not supported yet")
	return nil, nil
}

// CheckMounts checks v.spec.Mounts
func (v *Validator) CheckMounts() (msgs []string) {
	logrus.Debugf("check mounts")

	supportedTypes, err := supportedMountTypes(v.platform, v.HostSpecific)
	if err != nil {
		msgs = append(msgs, err.Error())
		return
	}

	for _, mount := range v.spec.Mounts {
		if supportedTypes != nil {
			if !supportedTypes[mount.Type] {
				msgs = append(msgs, fmt.Sprintf("Unsupported mount type %q", mount.Type))
			}
		}

		if !filepath.IsAbs(mount.Destination) {
			msgs = append(msgs, fmt.Sprintf("destination %v is not an absolute path", mount.Destination))
		}
	}

	return
}

// CheckPlatform checks v.platform
func (v *Validator) CheckPlatform() (msgs []string) {
	logrus.Debugf("check platform")

	if v.platform != "linux" && v.platform != "solaris" && v.platform != "windows" {
		msgs = append(msgs, fmt.Sprintf("platform %q is not supported", v.platform))
		return
	}

	if v.platform == "windows" {
		if v.spec.Windows == nil {
			msgs = append(msgs, "'windows' MUST be set when platform is `windows`")
		}
	}

	return
}

// CheckLinux checks v.spec.Linux
func (v *Validator) CheckLinux() (msgs []string) {
	logrus.Debugf("check linux")

	var typeList = map[rspec.LinuxNamespaceType]struct {
		num      int
		newExist bool
	}{
		rspec.PIDNamespace:     {0, false},
		rspec.NetworkNamespace: {0, false},
		rspec.MountNamespace:   {0, false},
		rspec.IPCNamespace:     {0, false},
		rspec.UTSNamespace:     {0, false},
		rspec.UserNamespace:    {0, false},
		rspec.CgroupNamespace:  {0, false},
	}

	for index := 0; index < len(v.spec.Linux.Namespaces); index++ {
		ns := v.spec.Linux.Namespaces[index]
		if !namespaceValid(ns) {
			msgs = append(msgs, fmt.Sprintf("namespace %v is invalid.", ns))
		}

		tmpItem := typeList[ns.Type]
		tmpItem.num = tmpItem.num + 1
		if tmpItem.num > 1 {
			msgs = append(msgs, fmt.Sprintf("duplicated namespace %q", ns.Type))
		}

		if len(ns.Path) == 0 {
			tmpItem.newExist = true
		}
		typeList[ns.Type] = tmpItem
	}

	if (len(v.spec.Linux.UIDMappings) > 0 || len(v.spec.Linux.GIDMappings) > 0) && !typeList[rspec.UserNamespace].newExist {
		msgs = append(msgs, "UID/GID mappings requires a new User namespace to be specified as well")
	} else if len(v.spec.Linux.UIDMappings) > 5 {
		msgs = append(msgs, "Only 5 UID mappings are allowed (linux kernel restriction).")
	} else if len(v.spec.Linux.GIDMappings) > 5 {
		msgs = append(msgs, "Only 5 GID mappings are allowed (linux kernel restriction).")
	}

	for k := range v.spec.Linux.Sysctl {
		if strings.HasPrefix(k, "net.") && !typeList[rspec.NetworkNamespace].newExist {
			msgs = append(msgs, fmt.Sprintf("Sysctl %v requires a new Network namespace to be specified as well", k))
		}
		if strings.HasPrefix(k, "fs.mqueue.") {
			if !typeList[rspec.MountNamespace].newExist || !typeList[rspec.IPCNamespace].newExist {
				msgs = append(msgs, fmt.Sprintf("Sysctl %v requires a new IPC namespace and Mount namespace to be specified as well", k))
			}
		}
	}

	if v.platform == "linux" && !typeList[rspec.UTSNamespace].newExist && v.spec.Hostname != "" {
		msgs = append(msgs, fmt.Sprintf("On Linux, hostname requires a new UTS namespace to be specified as well"))
	}

	for index := 0; index < len(v.spec.Linux.Devices); index++ {
		if !deviceValid(v.spec.Linux.Devices[index]) {
			msgs = append(msgs, fmt.Sprintf("device %v is invalid.", v.spec.Linux.Devices[index]))
		}
	}

	if v.spec.Linux.Resources != nil {
		ms := v.CheckLinuxResources()
		msgs = append(msgs, ms...)
	}

	if v.spec.Linux.Seccomp != nil {
		ms := v.CheckSeccomp()
		msgs = append(msgs, ms...)
	}

	switch v.spec.Linux.RootfsPropagation {
	case "":
	case "private":
	case "rprivate":
	case "slave":
	case "rslave":
	case "shared":
	case "rshared":
	case "unbindable":
	case "runbindable":
	default:
		msgs = append(msgs, "rootfsPropagation must be empty or one of \"private|rprivate|slave|rslave|shared|rshared|unbindable|runbindable\"")
	}

	for _, maskedPath := range v.spec.Linux.MaskedPaths {
		if !strings.HasPrefix(maskedPath, "/") {
			msgs = append(msgs, fmt.Sprintf("maskedPath %v is not an absolute path", maskedPath))
		}
	}

	for _, readonlyPath := range v.spec.Linux.ReadonlyPaths {
		if !strings.HasPrefix(readonlyPath, "/") {
			msgs = append(msgs, fmt.Sprintf("readonlyPath %v is not an absolute path", readonlyPath))
		}
	}

	return
}

// CheckLinuxResources checks v.spec.Linux.Resources
func (v *Validator) CheckLinuxResources() (msgs []string) {
	logrus.Debugf("check linux resources")

	r := v.spec.Linux.Resources
	if r.Memory != nil {
		if r.Memory.Limit != nil && r.Memory.Swap != nil && uint64(*r.Memory.Limit) > uint64(*r.Memory.Swap) {
			msgs = append(msgs, fmt.Sprintf("Minimum memoryswap should be larger than memory limit"))
		}
		if r.Memory.Limit != nil && r.Memory.Reservation != nil && uint64(*r.Memory.Reservation) > uint64(*r.Memory.Limit) {
			msgs = append(msgs, fmt.Sprintf("Minimum memory limit should be larger than memory reservation"))
		}
	}
	if r.Network != nil && v.HostSpecific {
		var exist bool
		interfaces, err := net.Interfaces()
		if err != nil {
			msgs = append(msgs, err.Error())
			return
		}
		for _, prio := range r.Network.Priorities {
			exist = false
			for _, ni := range interfaces {
				if prio.Name == ni.Name {
					exist = true
					break
				}
			}
			if !exist {
				msgs = append(msgs, fmt.Sprintf("Interface %s does not exist currently", prio.Name))
			}
		}
	}

	return
}

// CheckSeccomp checkc v.spec.Linux.Seccomp
func (v *Validator) CheckSeccomp() (msgs []string) {
	logrus.Debugf("check linux seccomp")

	s := v.spec.Linux.Seccomp
	if !seccompActionValid(s.DefaultAction) {
		msgs = append(msgs, fmt.Sprintf("seccomp defaultAction %q is invalid.", s.DefaultAction))
	}
	for index := 0; index < len(s.Syscalls); index++ {
		if !syscallValid(s.Syscalls[index]) {
			msgs = append(msgs, fmt.Sprintf("syscall %v is invalid.", s.Syscalls[index]))
		}
	}
	for index := 0; index < len(s.Architectures); index++ {
		switch s.Architectures[index] {
		case rspec.ArchX86:
		case rspec.ArchX86_64:
		case rspec.ArchX32:
		case rspec.ArchARM:
		case rspec.ArchAARCH64:
		case rspec.ArchMIPS:
		case rspec.ArchMIPS64:
		case rspec.ArchMIPS64N32:
		case rspec.ArchMIPSEL:
		case rspec.ArchMIPSEL64:
		case rspec.ArchMIPSEL64N32:
		case rspec.ArchPPC:
		case rspec.ArchPPC64:
		case rspec.ArchPPC64LE:
		case rspec.ArchS390:
		case rspec.ArchS390X:
		case rspec.ArchPARISC:
		case rspec.ArchPARISC64:
		default:
			msgs = append(msgs, fmt.Sprintf("seccomp architecture %q is invalid", s.Architectures[index]))
		}
	}

	return
}

// CapValid checks whether a capability is valid
func CapValid(c string, hostSpecific bool) error {
	isValid := false

	if !strings.HasPrefix(c, "CAP_") {
		return fmt.Errorf("capability %s must start with CAP_", c)
	}
	for _, cap := range capability.List() {
		if c == fmt.Sprintf("CAP_%s", strings.ToUpper(cap.String())) {
			if hostSpecific && cap > LastCap() {
				return fmt.Errorf("CAP_%s is not supported on the current host", c)
			}
			isValid = true
			break
		}
	}

	if !isValid {
		return fmt.Errorf("Invalid capability: %s", c)
	}
	return nil
}

// LastCap return last cap of system
func LastCap() capability.Cap {
	last := capability.CAP_LAST_CAP
	// hack for RHEL6 which has no /proc/sys/kernel/cap_last_cap
	if last == capability.Cap(63) {
		last = capability.CAP_BLOCK_SUSPEND
	}

	return last
}

func envValid(env string) bool {
	items := strings.Split(env, "=")
	if len(items) < 2 {
		return false
	}
	for i, ch := range strings.TrimSpace(items[0]) {
		if !unicode.IsDigit(ch) && !unicode.IsLetter(ch) && ch != '_' {
			return false
		}
		if i == 0 && unicode.IsDigit(ch) {
			logrus.Warnf("Env %v: variable name beginning with digit is not recommended.", env)
		}
	}
	return true
}

func (v *Validator) rlimitValid(rlimit rspec.POSIXRlimit) (msgs []string) {
	if rlimit.Hard < rlimit.Soft {
		msgs = append(msgs, fmt.Sprintf("hard limit of rlimit %s should not be less than soft limit", rlimit.Type))
	}

	if v.platform == "linux" {
		for _, val := range defaultRlimits {
			if val == rlimit.Type {
				return
			}
		}
		msgs = append(msgs, fmt.Sprintf("rlimit type %q is invalid", rlimit.Type))
	} else {
		logrus.Warnf("process.rlimits validation not yet implemented for platform %q", v.platform)
	}

	return
}

func namespaceValid(ns rspec.LinuxNamespace) bool {
	switch ns.Type {
	case rspec.PIDNamespace:
	case rspec.NetworkNamespace:
	case rspec.MountNamespace:
	case rspec.IPCNamespace:
	case rspec.UTSNamespace:
	case rspec.UserNamespace:
	case rspec.CgroupNamespace:
	default:
		return false
	}

	if ns.Path != "" && !filepath.IsAbs(ns.Path) {
		return false
	}

	return true
}

func deviceValid(d rspec.LinuxDevice) bool {
	switch d.Type {
	case "b", "c", "u":
		if d.Major <= 0 || d.Minor <= 0 {
			return false
		}
	case "p":
		if d.Major > 0 || d.Minor > 0 {
			return false
		}
	default:
		return false
	}
	return true
}

func seccompActionValid(secc rspec.LinuxSeccompAction) bool {
	switch secc {
	case "":
	case rspec.ActKill:
	case rspec.ActTrap:
	case rspec.ActErrno:
	case rspec.ActTrace:
	case rspec.ActAllow:
	default:
		return false
	}
	return true
}

func syscallValid(s rspec.LinuxSyscall) bool {
	if !seccompActionValid(s.Action) {
		return false
	}
	for index := 0; index < len(s.Args); index++ {
		arg := s.Args[index]
		switch arg.Op {
		case rspec.OpNotEqual:
		case rspec.OpLessThan:
		case rspec.OpLessEqual:
		case rspec.OpEqualTo:
		case rspec.OpGreaterEqual:
		case rspec.OpGreaterThan:
		case rspec.OpMaskedEqual:
		default:
			return false
		}
	}
	return true
}

func isStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct
}

func isStructPtr(t reflect.Type) bool {
	return t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct
}

func checkMandatoryUnit(field reflect.Value, tagField reflect.StructField, parent string) (msgs []string) {
	mandatory := !strings.Contains(tagField.Tag.Get("json"), "omitempty")
	switch field.Kind() {
	case reflect.Ptr:
		if mandatory && field.IsNil() {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
		}
	case reflect.String:
		if mandatory && (field.Len() == 0) {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
		}
	case reflect.Slice:
		if mandatory && (field.IsNil() || field.Len() == 0) {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
			return
		}
		for index := 0; index < field.Len(); index++ {
			mValue := field.Index(index)
			if mValue.CanInterface() {
				msgs = append(msgs, checkMandatory(mValue.Interface())...)
			}
		}
	case reflect.Map:
		if mandatory && (field.IsNil() || field.Len() == 0) {
			msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty.", parent, tagField.Name))
			return msgs
		}
		keys := field.MapKeys()
		for index := 0; index < len(keys); index++ {
			mValue := field.MapIndex(keys[index])
			if mValue.CanInterface() {
				msgs = append(msgs, checkMandatory(mValue.Interface())...)
			}
		}
	default:
	}

	return
}

func checkMandatory(obj interface{}) (msgs []string) {
	objT := reflect.TypeOf(obj)
	objV := reflect.ValueOf(obj)
	if isStructPtr(objT) {
		objT = objT.Elem()
		objV = objV.Elem()
	} else if !isStruct(objT) {
		return
	}

	for i := 0; i < objT.NumField(); i++ {
		t := objT.Field(i).Type
		if isStructPtr(t) && objV.Field(i).IsNil() {
			if !strings.Contains(objT.Field(i).Tag.Get("json"), "omitempty") {
				msgs = append(msgs, fmt.Sprintf("'%s.%s' should not be empty", objT.Name(), objT.Field(i).Name))
			}
		} else if (isStruct(t) || isStructPtr(t)) && objV.Field(i).CanInterface() {
			msgs = append(msgs, checkMandatory(objV.Field(i).Interface())...)
		} else {
			msgs = append(msgs, checkMandatoryUnit(objV.Field(i), objT.Field(i), objT.Name())...)
		}

	}
	return
}

// CheckMandatoryFields checks mandatory field of container's config file
func (v *Validator) CheckMandatoryFields() []string {
	logrus.Debugf("check mandatory fields")

	return checkMandatory(v.spec)
}
