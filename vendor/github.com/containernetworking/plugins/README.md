[![Build Status](https://travis-ci.org/containernetworking/plugins.svg?branch=master)](https://travis-ci.org/containernetworking/plugins)

# plugins
Some CNI network plugins, maintained by the containernetworking team. For more information, see the individual READMEs.

Read [CONTRIBUTING](CONTRIBUTING.md) for build and test instructions.

## Plugins supplied:
### Main: interface-creating
* `bridge`: Creates a bridge, adds the host and the container to it.
* `ipvlan`: Adds an [ipvlan](https://www.kernel.org/doc/Documentation/networking/ipvlan.txt) interface in the container.
* `loopback`: Set the state of loopback interface to up.
* `macvlan`: Creates a new MAC address, forwards all traffic to that to the container.
* `ptp`: Creates a veth pair.
* `vlan`: Allocates a vlan device.
* `host-device`: Move an already-existing device into a container.
#### Windows: windows specific
* `win-bridge`: Creates a bridge, adds the host and the container to it.
* `win-overlay`: Creates an overlay interface to the container.
### IPAM: IP address allocation
* `dhcp`: Runs a daemon on the host to make DHCP requests on behalf of the container
* `host-local`: Maintains a local database of allocated IPs
* `static`:  Allocate a static IPv4/IPv6 addresses to container and it's useful in debugging purpose.

### Meta: other plugins
* `flannel`: Generates an interface corresponding to a flannel config file
* `tuning`: Tweaks sysctl parameters of an existing interface
* `portmap`: An iptables-based portmapping plugin. Maps ports from the host's address space to the container.
* `bandwidth`: Allows bandwidth-limiting through use of traffic control tbf (ingress/egress).
* `sbr`: A plugin that configures source based routing for an interface (from which it is chained).
* `firewall`: A firewall plugin which uses iptables or firewalld to add rules to allow traffic to/from the container.

### Sample
The sample plugin provides an example for building your own plugin.
