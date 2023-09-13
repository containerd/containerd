# Release Notes

## 0.4.0

- Pass the ttRPC receiving context from the Stub to each NRI request handler
of the plugin.
- Fix Stub/Plugin UpdateContainer interface to pass the resource update to
the UpdateContainer NRI request handler of the plugin as the last argument.
- All plugins need to be updated to reflect the above changes in any NRI
request handler they implement.
- NRI plugins can now add rlimits

## 0.3.0

- Eliminate the global NRI configuration file, replacing any remaining
configuration options with corresponding programmatic options for runtimes.
- Change default socket path from /var/run/nri.sock to /var/run/nri/nri.sock.
- Make plugin timeouts configurable on the runtime side.
- Plugins should be API-compatible between 0.2.0 and 0.3.0, but either the
runtime needs to be configured to use the old NRI socket path, or 0.2.0 plugins
need to be configured to use the new default NRI socket path.

## 0.2.0

- Replace the v0.1.0 CNI like plugin interface with JSON message exchange on
stdin and stdout with external daemon-like plugins and a protobuf-defined
protocol with ttRPC bindings for communicating with the runtime.
- Allow plugins to track the state of (CRI) pods and containers.
- Allow plugins to make changes to a selected subset of container parameters
during container creation, update, and stopping of (other) containers.
- All 0.1.0 plugins are incompatible with 0.2.0, although
[an experimental adapter plugin](plugins/v010-adapter) is provided to bridge
between any existing 0.1.0 plugins and the current NRI APIs.
