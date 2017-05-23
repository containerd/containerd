package ocicni

const (
	// DefaultInterfaceName is the string to be used for the interface name inside the net namespace
	DefaultInterfaceName = "eth0"
	// CNIPluginName is the default name of the plugin
	CNIPluginName = "cni"
	// DefaultNetDir is the place to look for CNI Network
	DefaultNetDir = "/etc/cni/net.d"
	// DefaultCNIDir is the place to look for cni config files
	DefaultCNIDir = "/opt/cni/bin"
	// VendorCNIDirTemplate is the template for looking up vendor specific cni config/executable files
	VendorCNIDirTemplate = "%s/opt/%s/bin"
)

// CNIPlugin is the interface that needs to be implemented by a plugin
type CNIPlugin interface {
	// Name returns the plugin's name. This will be used when searching
	// for a plugin by name, e.g.
	Name() string

	// SetUpPod is the method called after the infra container of
	// the pod has been created but before the other containers of the
	// pod are launched.
	SetUpPod(netnsPath string, namespace string, name string, containerID string) error

	// TearDownPod is the method called before a pod's infra container will be deleted
	TearDownPod(netnsPath string, namespace string, name string, containerID string) error

	// Status is the method called to obtain the ipv4 or ipv6 addresses of the container
	GetContainerNetworkStatus(netnsPath string, namespace string, name string, containerID string) (string, error)

	// NetworkStatus returns error if the network plugin is in error state
	Status() error
}
