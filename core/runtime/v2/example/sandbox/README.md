# Sandbox API Shim Example

This package provides a minimal in-memory example of a Runtime v2 shim that also implements the Sandbox API.

It is intended as a small starting point for runtime authors who want to understand how a shim can:

- register both the `Task` and `Sandbox` TTRPC services
- handle a basic sandbox lifecycle
- report sandbox platform and status
- expose a runnable shim entrypoint
- be tested directly without running a full containerd instance

## Behavior

The example keeps all sandbox state in memory and does not launch real workloads.

Implemented sandbox RPC behavior:

- `CreateSandbox` stores sandbox metadata
- `StartSandbox` marks the sandbox as running and returns the current process pid
- `Platform` reports the current Go runtime platform
- `SandboxStatus` reports state and basic metadata
- `WaitSandbox` blocks until `StopSandbox` is called
- `PingSandbox` verifies the sandbox exists
- `ShutdownSandbox` removes the sandbox and triggers shim shutdown

The Task service methods are intentionally left as `ErrNotImplemented`, except for `Shutdown`.

## Build

```bash
go build ./core/runtime/v2/example/sandbox/cmd
```

## Test

```bash
go test ./core/runtime/v2/example/sandbox
```

## Notes

This is a teaching example, not a production shim. Real implementations need persistent state handling, task lifecycle support, robust process management, metrics, and integration with containerd runtime expectations.