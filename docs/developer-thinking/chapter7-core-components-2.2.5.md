# 《containerd 原理剖析与实战》第 7 章伴读

> **适配版本：containerd 2.2.5**
> **对应原书章节：第 7 章 containerd 核心组件解析**
> **源码基线：用户提供的 `containerd-2.2.5` 源码包**

---

## 阅读说明

这一章是全书最重要的一章。前面看到的镜像、容器、Task、Snapshot、CRI、CNI，到了这里要放回 containerd 的插件图和调用链。

先建立总架构：

```text
Clients
├── ctr / nerdctl / Go client       Native API
├── kubelet / crictl                CRI API
└── remote plugins                  proxy gRPC
        │
        ▼
containerd daemon
├── gRPC API layer
├── Services
│   ├── Containers
│   ├── Images
│   ├── Content
│   ├── Snapshots
│   ├── Tasks
│   ├── Events
│   ├── Leases
│   ├── Transfer
│   └── CRI
├── Metadata DB + GC
├── Backend plugins
│   ├── Content store
│   ├── Snapshotters
│   ├── Differs
│   ├── Runtime v2 task manager
│   ├── Shim manager
│   └── NRI
└── shim processes
        │ ttrpc / socket
        ▼
OCI runtime / VM runtime
```

containerd 的核心不是一个巨大 `main()`，而是：

```text
插件注册表 + 依赖图 + 统一 API + 后端实现
```

---

## 7.1 containerd 架构总览

### 7.1.1 启动主流程

从 `containerd` 二进制启动到可接受请求，大致经历：

```text
cmd/containerd
  ↓
解析命令行与 config.toml
  ↓
配置版本迁移
  ↓
创建 root/state/temp 目录
  ↓
LoadPlugins
  ↓
按 Requires 构造并初始化插件依赖图
  ↓
收集 gRPC / TCP gRPC / ttrpc services
  ↓
注册服务
  ↓
监听 Unix socket / metrics / debug
  ↓
发送 systemd READY=1
```

关键入口：

- `cmd/containerd/command/main.go`
- `cmd/containerd/server/server.go`
- `cmd/containerd/server/config/`
- `plugin/registry` 来自 containerd plugin 模块

#### 7.1.1.1 配置先迁移再应用

`server.New()` 首先检查：

```go
if currentVersion < version.ConfigVersion {
    config.MigrateConfig(ctx)
}
```

2.2.5 当前配置版本是 3。每个插件可注册 `ConfigMigration`，将旧字段迁移到新插件结构。CRI image/runtime/server 拆分就是典型例子。

配置迁移只解决结构转换，不保证业务含义完全符合新版本。升级后仍需人工审查 deprecated 字段和默认值变化。

#### 7.1.1.2 插件 root/state

containerd 给每个插件设置属性：

```text
io.containerd.plugin.root
io.containerd.plugin.state
io.containerd.plugin.grpc.address
io.containerd.plugin.ttrpc.address
```

默认目录按插件 URI 派生：

```text
/var/lib/containerd/<type>.<id>
/run/containerd/<type>.<id>
```

插件通过 `InitContext.Properties` 获取，不应自行假设全局路径。

#### 7.1.1.3 Root、State 与 Shim 状态

源码中特别说明：即使用户把 `state` 改到其他路径，部分 shim socket/FIFO 历史上仍依赖默认 state 根，因此 server 还会确保默认 `/run/containerd` 存在。

这提醒我们：

> 修改 containerd state 路径并不等于所有运行时组件都会完全搬迁，必须按版本检查 shim/cio 的路径约束。

---

### 7.1.2 插件类型

`plugins/types.go` 在 2.2.5 中定义的主要类型：

| 类型 | 作用 |
|---|---|
| `io.containerd.internal.v1` | daemon 内部基础插件 |
| `io.containerd.service.v1` | 内部 service 聚合 |
| `io.containerd.grpc.v1` | 对外 gRPC service |
| `io.containerd.ttrpc.v1` | daemon 内 ttrpc service |
| `io.containerd.content.v1` | content store |
| `io.containerd.metadata.v1` | metadata DB |
| `io.containerd.snapshotter.v1` | snapshotter |
| `io.containerd.differ.v1` | diff/apply |
| `io.containerd.runtime.v2` | Runtime v2 task manager |
| `io.containerd.shim.v1` | shim manager/service |
| `io.containerd.event.v1` | event exchange |
| `io.containerd.lease.v1` | lease manager |
| `io.containerd.gc.v1` | GC 调度/策略 |
| `io.containerd.transfer.v1` | 镜像与对象传输服务 |
| `io.containerd.cri.v1` | CRI image/runtime service |
| `io.containerd.nri.v1` | NRI adaptation |
| `io.containerd.sandbox.*` | sandbox store/controller |
| `io.containerd.mount-*` | mount manager/handler |
| `io.containerd.http.v1` | HTTP handler |

源码仍保留 `io.containerd.runtime.v1` 的类型常量，主要用于类型命名兼容；containerd 2.x 已移除旧 Runtime v1 实现与旧 shim 架构。不要因为常量仍存在就认为 `io.containerd.runtime.v1.linux` 仍可使用。

#### 7.1.2.1 Type、ID、URI

```text
Type = io.containerd.snapshotter.v1
ID   = overlayfs
URI  = io.containerd.snapshotter.v1.overlayfs
```

配置：

```toml
[plugins."io.containerd.snapshotter.v1.overlayfs"]
```

命令：

```bash
ctr plugins list --detailed | grep 'io.containerd.snapshotter.v1.overlayfs'
```

三者完全对应。

---

### 7.1.3 插件依赖图

插件注册时声明：

```go
Requires: []plugin.Type{...}
```

containerd 加载器会按依赖排序初始化。示意：

```text
content plugin ───────┐
snapshotter plugins ──┼─→ metadata/bolt
 event plugin ────────┘        │
                               ├─→ services
shim manager ─→ runtime.v2.task│       │
                               └───────┴─→ gRPC APIs
```

这带来几个结论：

1. Go 文件 `init()` 负责注册，不代表最终启动顺序。
2. 一个插件可依赖某一“类型”的任一或多个实例。
3. `GetSingle` 要求该类型唯一；`GetByID` 精确取某 ID；`GetByType` 取全部。
4. 底层插件 error 会传导到上层依赖。
5. 插件可以被 disabled、skipped 或因平台不匹配不加载。

#### 7.1.3.1 `ctr plugins list` 的价值

```bash
ctr plugins list
```

典型列：

```text
TYPE  ID  PLATFORMS  STATUS
```

排障顺序：

```text
目标 gRPC service error
  ↓
看其依赖 service
  ↓
看 metadata/snapshot/runtime/shim 等后端
  ↓
看最早发生错误的插件
```

不要只盯最上层 `cri` 插件的“dependency failed”。

---

### 7.1.4 Daemon 与 Shim 为什么分离

如果 containerd 直接作为所有容器进程父管理者：

- daemon 升级/重启会影响容器；
- 标准输入输出与退出状态难持久管理；
- 不同 runtime 的生命周期逻辑耦合进 daemon；
- 每个容器的 cgroup、OOM、exec、signal 逻辑复杂。

shim 的作用：

```text
containerd daemon
  │ 管理与调度
  ▼
shim
  ├── 作为容器进程管理代理
  ├── 持有 IO/FIFO/socket
  ├── 收集 exit status
  ├── 调用 OCI/VM runtime
  └── 允许 daemon 重启后重连
```

containerd 重启并不要求业务容器一起退出，核心依赖就是 shim 独立存活和 `LoadExistingShims()` 重连。

---

## 7.2 containerd API 和 Core

### 7.2.1 gRPC API

#### 7.2.1.1 API 目录

2.2.5 服务 protobuf 位于：

```text
api/services/
├── containers
├── content
├── diff
├── events
├── images
├── introspection
├── leases
├── mounts
├── namespaces
├── sandbox
├── snapshots
├── streaming
├── tasks
├── transfer
└── version
```

生成的 API 模块来自：

```text
github.com/containerd/containerd/api
```

主仓库 Go module 路径是：

```text
github.com/containerd/containerd/v2
```

这两个 module path 不要混淆。

#### 7.2.1.2 Native API 与 CRI API

Native API 对象：

```text
Container
Task
Image
Content
Snapshot
Lease
Namespace
```

CRI API 对象：

```text
PodSandbox
Container
Image
RuntimeConfig
ContainerStats
```

CRI facade 会把 CRI 对象映射到 Native API。虽然都有 `Container`，protobuf 类型、状态机和字段并不相同。

#### 7.2.1.3 Namespace interceptor

server 构建 gRPC 时添加：

```text
streamNamespaceInterceptor
unaryNamespaceInterceptor
```

Native API 请求通常通过 gRPC metadata 携带 containerd namespace。客户端使用：

```go
ctx = namespaces.WithNamespace(ctx, "demo")
```

缺少 namespace 时，部分 API 会报错或由客户端默认值补充。CRI 内部 client 默认使用 `k8s.io`。

#### 7.2.1.4 gRPC 与 ttrpc

containerd 对外客户端主要使用 gRPC；daemon 与 shim 的 Task API 主要使用 ttrpc。

```text
gRPC
  功能完整、protobuf、HTTP/2，适合 daemon API

ttrpc
  轻量 RPC，面向 shim/低开销 Unix socket 通信
```

二者都使用 protobuf，但传输和 server/client 实现不同。

#### 7.2.1.5 服务注册

`server.New()` 定义接口：

```go
type grpcService interface {
    Register(*grpc.Server) error
}

type tcpService interface {
    RegisterTCP(*grpc.Server) error
}

type ttrpcService interface {
    RegisterTTRPC(*ttrpc.Server) error
}
```

插件初始化结果只要实现这些接口，就会被收集并注册到对应 server。这是插件把内部能力暴露为 API 的关键桥梁。

#### 7.2.1.6 Unix Socket 与 TCP gRPC

默认 gRPC：

```text
/run/containerd/containerd.sock
```

可配置 TCP listener 和 TLS/mTLS。源码支持证书、私钥、CA，配置客户端证书验证。

containerd Socket 权限相当敏感：能够调用 API 的用户通常可创建 privileged 容器、挂载宿主路径，权限接近 root。不要把无认证 TCP API 暴露到不可信网络。

---

### 7.2.2 Services

#### 7.2.2.1 Service 是 API 与 Backend 的适配层

典型：

```text
gRPC Images service
  ↓
ImageService interface
  ↓
metadata image store
  ↓
content descriptors
```

Service 层负责：

- protobuf 与内部类型转换；
- 参数校验；
- namespace 提取；
- filter；
- event 发布；
- 调用 backend；
- error 到 gRPC status 映射。

Backend 负责真正存储或运行。

#### 7.2.2.2 Containers Service

Container 是 metadata：

```text
ID
Image
Runtime{Name, Options}
Spec
Snapshotter
SnapshotKey
Labels
Extensions
SandboxID
```

创建 Container 不等于创建进程。Containers service 只维护定义；Tasks service 才创建运行实例。

#### 7.2.2.3 Tasks Service

Tasks service 将 container metadata 转为 runtime CreateOpts：

- 读取 OCI Spec；
- 取得 rootfs mounts；
- 传 runtime 名和 options；
- 创建 Task；
- 管理 Start/Kill/Delete/Exec/Stats/Pause/Resume。

它是 Native API 到 Runtime v2 的主要入口。

#### 7.2.2.4 Images、Content 与 Transfer

传统 client pull 会协调 resolver、content、image、unpack；2.x 还引入 Transfer Service，把跨 registry/content/image 的传输能力集中为插件服务。

CRI ImageService 默认倾向使用 Transfer Service，除非 `use_local_image_pull=true`。

#### 7.2.2.5 Events

containerd 通过 event exchange 发布：

```text
/containerd/images/create
/containerd/containers/create
/containerd/tasks/start
/containerd/tasks/exit
/containerd/snapshots/prepare
...
```

客户端：

```bash
ctr events
```

事件用于观察与解耦，不应被当作唯一持久状态。订阅者掉线后仍要通过 list/get API 重建当前状态。

#### 7.2.2.6 Leases

Lease 把长操作期间的 content/snapshot 变成 GC root。Service 提供 create/delete/list/add resource。

设计原则：

```text
状态 API 是真相
事件是变化通知
lease 是暂时所有权
GC labels 是引用关系
```

---

### 7.2.3 Metadata

#### 7.2.3.1 Bolt Metadata 插件

注册：

```text
io.containerd.metadata.v1.bolt
```

依赖：

```text
ContentPlugin
EventPlugin
SnapshotPlugin
```

它把多个后端包装成 namespace-aware 的 metadata 视图，并存入：

```text
<plugin-root>/meta.db
```

#### 7.2.3.2 为什么 Snapshotter 还需要 Metadata DB

具体 snapshotter 可能有自己的 metastore，但 containerd metadata 层还要维护：

- namespace 与 snapshot 的关系；
- labels；
- GC reference；
- 与 container/image 的关联；
- 多 snapshotter 的统一视图。

因此不能认为 snapshotter 自己的 DB 就包含 containerd 所有对象关系。

#### 7.2.3.3 Content sharing policy

```toml
[plugins."io.containerd.metadata.v1.bolt"]
  content_sharing_policy = "shared"
  no_sync = false
```

`shared` 与 `isolated` 的差异主要是 namespace 对已存在 digest 的访问证明语义。底层 blob 仍可共享。

#### 7.2.3.4 GC 的引用图

metadata 通过 labels 和 buckets 构建对象图：

```text
namespace
├── image → target content
├── container → snapshot + content/spec
├── snapshot → parent
├── lease → resources
└── ingest → expected content
```

GC 的难点不是删除文件，而是判断“谁还在引用谁”。

#### 7.2.3.5 bbolt 同步选项

`no_sync=true` 会启用 `NoSync` 与 `NoGrowSync`，源码明确警告崩溃时可能丢数据。生产中不应只为降低延迟而无评估开启。

---

## 7.3 containerd Backend

### 7.3.1 containerd 中的 proxy plugins

#### 7.3.1.1 为什么需要 Proxy Plugin

并非所有后端都必须编译进 containerd。外部进程可通过 gRPC 实现某类服务，containerd 注册一个 proxy adapter：

```text
containerd
  │ proxy plugin
  │ Unix/TCP gRPC
  ▼
external snapshotter/content/diff service
```

常见场景：

- remote snapshotter；
- 特殊存储后端；
- 独立升级和崩溃隔离；
- 厂商闭源实现；
- 与 daemon 不同语言/发布周期。

#### 7.3.1.2 配置结构

server config 中 ProxyPlugin 包含：

```text
Type
Address
Platform
Exports
Capabilities
```

示意：

```toml
[proxy_plugins.stargz]
  type = "snapshot"
  address = "/run/containerd-stargz-grpc/containerd-stargz-grpc.sock"
```

加载器会根据 `type` 注册相应插件类型的 proxy。具体允许值与 adapter 代码为准。

#### 7.3.1.3 Proxy 与 Shim 的区别

```text
Proxy plugin
  代理 containerd Backend API，如 Snapshotter

Shim
  代理容器 Task 生命周期与 runtime
```

二者都是外部进程，但接口和生命周期完全不同。

#### 7.3.1.4 故障模型

外部 plugin 还引入：

- socket 不存在；
- 启动顺序；
- API 版本不兼容；
- 请求超时；
- 外部 daemon 重启；
- containerd 插件初始化 dependency error。

systemd 中通常要显式设置依赖和重启策略。

---

### 7.3.2 containerd 中的 Runtime 和 shim

#### 7.3.2.1 Runtime v2 组成

2.2.5 的默认 Linux runtime：

```text
io.containerd.runc.v2
```

组件：

```text
Runtime v2 TaskManager plugin
  io.containerd.runtime.v2.task
        │
        ▼
ShimManager plugin/service
        │
        ▼
containerd-shim-runc-v2
        │
        ▼
runc
```

关键源码：

- `core/runtime/v2/task_manager.go`
- `core/runtime/v2/shim_manager.go`
- `core/runtime/v2/bundle.go`
- `pkg/shim/`
- `cmd/containerd-shim-runc-v2/`

#### 7.3.2.2 runtime type 到 shim binary

Unix 命名规则近似：

```text
io.containerd.runc.v2
  ↓ 去掉 io.containerd. 前缀，拆 name/version
containerd-shim-runc-v2
```

源码：

```text
pkg/shim/util_unix.go
```

也可通过 runtime path 显式覆盖。

#### 7.3.2.3 Shim 不等于 runc 常驻进程

runc 通常是短命 CLI：

```text
runc create
runc start
runc exec
runc kill
runc delete
```

命令执行完 runc 进程退出，容器 init 继续运行。常驻管理的是 shim，它保存容器状态、IO 和退出事件。

#### 7.3.2.4 一容器一 shim 还是一 sandbox 一 shim

Runtime v2 协议支持 shim 管理多个 container/task。对 Sandboxed CRI，2.2.5 的 `ShimManager` 在 `CreateOpts.SandboxID` 非空且 sandbox 已存在时复用该 sandbox 的 shim bootstrap 参数；runc v2 shim 也识别 `io.containerd.runc.v2.group` 与 `io.kubernetes.cri.sandbox-id` annotation。普通非 sandbox 使用仍常表现为一个容器对应一个 shim。

不要把进程数作为固定 API 保证。应理解：

```text
shim 是 runtime service 实例
一个实例可按 runtime/sandbox 语义管理一个或多个 task
```

#### 7.3.2.5 containerd 重启后的 reconnect

Runtime v2 TaskManager 初始化时调用：

```go
shimManager.LoadExistingShims(ctx, state, root)
```

它扫描持久/状态信息，与仍运行的 shim 重连并恢复 task 管理。这是：

```text
systemctl restart containerd
```

通常不杀死正在运行容器的核心原因。

但如果 shim 自身死亡，容器与 IO/exit 管理可能受到不同程度影响，取决于 runtime 和容器进程父子关系。

---

### 7.3.3 containerd shim 规范

#### 7.3.3.1 Shim 的 API 面

Runtime v2 shim 通过 Task service 提供：

```text
Create
Start
Delete
Exec
ResizePty
State
Pause
Resume
Kill
Pids
CloseIO
Checkpoint
Update
Wait
Stats
Connect
Shutdown
```

不同 shim 可对部分能力返回 not implemented。

#### 7.3.3.2 Bundle

TaskManager 在创建 task 前构造 bundle：

```text
<state-or-root>/<namespace>/<id>/
├── config.json
├── rootfs/ 或 mount 描述
├── address
├── shim.pid
├── init.pid
├── log
└── runtime/shim 状态文件
```

具体布局由 Runtime v2 和 shim 实现决定。`config.json` 是 OCI Runtime Spec，不是 containerd daemon config。

#### 7.3.3.3 IO

containerd/shim 支持：

- stdin/stdout/stderr FIFO；
- terminal 模式；
- detach；
- close IO；
- exec process 独立 IO；
- streaming 类型 IO。

`Task.Start()` 后若调用方没有正确读取 stdout/stderr，管道背压可能阻塞业务进程。生产客户端不能忽略 IO 生命周期。

#### 7.3.3.4 Exit 与 Wait

正确时序：

```text
exitC, err := task.Wait(ctx)
  ↓
task.Start(ctx)
  ↓
status := <-exitC
```

先注册 Wait 再 Start，避免极短进程退出后客户端错过状态。shim 会收集 exit code、exitedAt 并发布 TaskExit event。

#### 7.3.3.5 Delete 的语义

Task Delete 与 Container Delete 分开：

```text
Task.Delete
  删除运行实例/runtime state

Container.Delete
  删除 metadata，可选清理 snapshot
```

必须先停止并删除 Task，再删 Container。强行删 metadata 不能替代 runtime cleanup。

---

### 7.3.4 shim 工作流程解析

#### 7.3.4.1 创建 Task 的真实调用链

```text
Client.Container.NewTask
  ↓
gRPC Tasks/Create
  ↓
Tasks service
  ├─ 读取 Container metadata/Spec/Runtime
  ├─ 取得 rootfs mounts
  └─ 调 Runtime v2 TaskManager.Create
        ↓
NewBundle
        ↓
MountManager.Activate
        ↓
ShimManager.Start
  ├─ 选择/查找 shim binary
  ├─ 启动或复用 shim
  ├─ 建立 ttrpc client
  └─ 保存 shim info
        ↓
shim Task.Create
        ↓
containerd-shim-runc-v2
  ├─ 保存 process state
  ├─ 准备 IO
  └─ 调 runc create
        ↓
runc/libcontainer
  ├─ clone namespaces
  ├─ cgroup
  ├─ mounts/rootfs
  ├─ capabilities/seccomp
  └─ 创建 paused/created init process
```

此时 Task 状态通常是 `CREATED`，应用入口尚未真正执行。

#### 7.3.4.2 Start

```text
Client Task.Start
  ↓
Tasks/Start
  ↓
shim Start
  ↓
runc start
  ↓
解除 created 阶段同步
  ↓
容器 init exec 用户命令
```

为什么 create/start 分离：

- 允许 runtime 在进程真正执行前完成必要准备、hook 或外部协调；默认 Sandboxed CRI 的 CNI ADD 已在 sandbox task 启动前执行，不能概括为“网络一定在此处配置”；
- 与 OCI runtime lifecycle 对齐；
- 客户端可先注册 Wait/IO；
- CRI CreateContainer/StartContainer 可分离。

#### 7.3.4.3 Exec

```text
Task.Exec(execID, processSpec, IO)
  ↓
shim 保存 exec process
  ↓
runc exec --process <spec>
```

Exec process 与 init process：

- 共享容器 namespace/cgroup；
- 有独立 PID、IO、exit status；
- 删除 Task 前要处理所有 exec；
- exec ID 在 task 内唯一。

#### 7.3.4.4 Kill 与 Stop

containerd 没有一个魔法“优雅停止”内核调用。上层通常：

```text
发送 SIGTERM
  ↓
等待 grace period
  ↓
仍未退出则 SIGKILL
```

CRI/kubelet 负责 termination grace period 语义；shim/runc 负责把 signal 发给 init 或全部进程。

#### 7.3.4.5 Delete 与清理

异常路径要逆序：

```text
shim task delete
shim shutdown/close
shim registry remove
mount deactivate
bundle delete
snapshot cleanup（上层）
metadata delete（上层）
```

`TaskManager.Create` 源码包含大量 defer 和 cleanup timeout，说明真正复杂的是失败恢复。

#### 7.3.4.6 Runtime feature validation

2.2.5 在 shim create 前会验证 OCI runtime features。原因是 runc 对未知特性可能静默忽略，而某些安全/语义特性不能接受“看似成功但未生效”。

这比旧版“只要 runc 命令退出 0 就算成功”更严格。

---

## 7.4 containerd 与 NRI

### 7.4.1 NRI 概述

NRI（Node Resource Interface）为节点级插件提供容器生命周期钩子，使插件能观察或调整：

- OCI/CRI 容器资源；
- CPU、内存、cpuset；
- devices；
- mounts；
- environment；
- annotations；
- Linux resources；
- 其他已运行容器的资源更新。

架构：

```text
containerd CRI/runtime
  │ lifecycle event/request
  ▼
NRI adaptation
  │ ttrpc/protobuf
  ▼
NRI plugins
  ├─ observe
  ├─ adjust new container
  ├─ update other containers
  └─ validate adjustments
```

NRI 不替代 CRI，也不替代 admission webhook。它工作在节点运行时执行阶段，更接近最终 OCI/runtime 资源。

#### 7.4.1.1 与 OCI hooks 的区别

| NRI | OCI Hook |
|---|---|
| 运行时级插件协议 | 单容器 OCI lifecycle 命令 |
| 能看到 Pod/Container 结构 | 主要看到 OCI state/config |
| 可更新其他已运行容器 | 通常只作用当前容器 |
| 插件有注册、同步和事件订阅 | hook 由 config.json 定义 |
| 与 CRI/containerd 集成 | 由 OCI runtime 在特定阶段执行 |

#### 7.4.1.2 适用场景

- CPU Manager/NUMA 资源优化；
- 设备注入；
- RDT/block I/O 类 QoS；
- sidecar/安全代理相关挂载；
- 运行时策略验证；
- 节点资源重平衡；
- 观测容器生命周期。

NRI 插件具有强权限，错误 adjustment 可导致容器无法启动或安全边界被破坏，必须按节点基础设施组件治理。

---

### 7.4.2 NRI 插件原理

#### 7.4.2.1 containerd NRI 插件

插件 URI：

```text
io.containerd.nri.v1.nri
```

配置结构源码：

```text
internal/nri/config.go
```

默认字段：

| 字段 | 含义 |
|---|---|
| `disable` | 禁用 containerd NRI 功能 |
| `socket_path` | 外部 NRI 插件连接的 Unix socket |
| `plugin_path` | 自动启动插件的搜索目录 |
| `plugin_config_path` | 插件配置目录 |
| `plugin_registration_timeout` | 注册超时 |
| `plugin_request_timeout` | 每次请求处理超时 |
| `disable_connections` | 禁止外部启动的插件主动连接 |
| `default_validator` | 内置 adjustment validator 配置 |

本源码 vendored NRI 版本：

```text
github.com/containerd/nri v0.11.0
```

#### 7.4.2.2 插件注册与排序

NRI plugin 有：

```text
plugin index
plugin name
```

index 用于确定调用顺序。多个插件依次 adjustment 时，后续插件看到并影响最终结果，冲突需要 validator/协议规则处理。

插件名和 index 可由：

- 可执行文件命名；
- 环境变量；
- `stub.WithPluginName`；
- `stub.WithPluginIdx`。

#### 7.4.2.3 连接模型

NRI 支持：

```text
containerd 自动扫描 plugin_path 并启动插件
或
外部插件连接 socket_path 注册
```

`disable_connections=true` 可禁止外部主动连接，只允许受 runtime 管理的插件，缩小攻击面。

#### 7.4.2.4 配置与 Synchronize

插件连接后：

```text
Configure(config, runtime, version)
  ↓
返回 EventMask
  ↓
Synchronize(existing pods, existing containers)
  ↓
开始接收 lifecycle events
```

Synchronize 让 containerd 重启或插件重连后，插件不只知道新容器，还能获得当前状态。

#### 7.4.2.5 生命周期接口

vendored `pkg/stub/stub.go` 定义：

```text
Configure
Synchronize
Shutdown
RunPodSandbox
UpdatePodSandbox
StopPodSandbox
RemovePodSandbox
PostUpdatePodSandbox
CreateContainer
StartContainer
UpdateContainer
StopContainer
RemoveContainer
PostCreateContainer
PostStartContainer
PostUpdateContainer
ValidateContainerAdjustment
```

插件无需实现全部接口，但至少实现一个，否则 `stub.New()` 返回错误。

#### 7.4.2.6 CreateContainer adjustment

接口：

```go
CreateContainer(
    context.Context,
    *api.PodSandbox,
    *api.Container,
) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
```

返回两类动作：

```text
ContainerAdjustment
  修改当前正在创建的容器

[]ContainerUpdate
  修改其他尚未停止的容器资源
```

这让 NRI 可以做资源联动，例如新高优先级容器创建时调整已有容器 cpuset。

#### 7.4.2.7 containerd 内部钩子

源码入口：

```text
internal/nri/nri.go
```

可看到 RunPodSandbox、CreateContainer、PostCreate、Start、PostStart、Update、PostUpdate、Stop、Remove 等方法。CRI 流程在关键阶段调用 NRI API，再把 adjustment 应用到 OCI Spec/runtime resources。

#### 7.4.2.8 Timeout 的意义

NRI 在容器关键路径上。若插件卡住：

```text
CreateContainer
  ↓
等待 NRI
  ↓
Pod 启动延迟或失败
```

所以配置 registration/request timeout。插件必须：

- 避免长时间同步网络调用；
- 设计缓存和降级；
- 对超时和重试保持幂等；
- 输出可关联 container/pod ID 的日志。

---

### 7.4.3 containerd 中启用 NRI 插件

#### 7.4.3.1 查看默认配置

```bash
containerd config default | sed -n '/io.containerd.nri.v1.nri/,/^\[/p'
```

示意：

```toml
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri/nri.sock"
  plugin_path = "/opt/nri/plugins"
  plugin_config_path = "/etc/nri/conf.d"
  plugin_registration_timeout = "5s"
  plugin_request_timeout = "2s"
  disable_connections = false
```

实际默认路径和 duration 应以生成配置为准。

#### 7.4.3.2 启动前检查

```bash
ctr plugins list | grep nri
ls -ld /opt/nri/plugins /etc/nri/conf.d /var/run/nri
journalctl -u containerd | grep -i nri
```

NRI 插件通常由 root 运行或能影响 root 级容器配置。目录应禁止普通用户写入，否则可通过放置恶意插件劫持容器启动。

#### 7.4.3.3 插件命名和配置

常见约定是以数字 index 开头：

```text
/opt/nri/plugins/10-my-plugin
/etc/nri/conf.d/10-my-plugin.conf
```

确切匹配规则以 NRI adaptation 代码为准。index 决定多个插件顺序，生产中应显式规划，避免不同厂商默认 index 冲突。

#### 7.4.3.4 验证

1. 启动 containerd；
2. 确认 NRI plugin 注册日志；
3. 创建测试 Pod/容器；
4. 检查插件收到 Configure/Synchronize/Create/Start；
5. 检查最终 OCI Spec/cgroup 是否符合 adjustment；
6. 删除容器，确认 Stop/Remove。

仅看到插件进程存在不代表它已成功注册和订阅事件。

---

### 7.4.4 containerd NRI 插件示例

下面是基于 vendored NRI v0.11.0 接口的“只观察、不修改”最小示例：

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/containerd/nri/pkg/api"
    "github.com/containerd/nri/pkg/stub"
)

type plugin struct{}

// 实现 CreateContainerInterface 即可订阅对应事件。
func (p *plugin) CreateContainer(
    ctx context.Context,
    pod *api.PodSandbox,
    ctr *api.Container,
) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
    fmt.Printf("create container: pod=%s container=%s\n", pod.GetId(), ctr.GetId())

    // nil adjustment：不修改当前容器。
    // nil updates：不修改其他容器。
    return nil, nil, nil
}

func main() {
    s, err := stub.New(
        &plugin{},
        stub.WithPluginName("observer"),
        stub.WithPluginIdx("10"),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := s.Run(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`go.mod`：

```go
module example.com/nri-observer

go 1.25

require github.com/containerd/nri v0.11.0
```

构建：

```bash
go build -o 10-observer .
sudo install -m 0755 10-observer /opt/nri/plugins/10-observer
```

示例只证明事件链。真正修改资源前，应阅读：

```text
vendor/github.com/containerd/nri/pkg/api
vendor/github.com/containerd/nri/pkg/stub
vendor/github.com/containerd/nri/plugins/default-validator
```

并为 adjustment 写单元测试。

#### 7.4.4.1 为什么返回 nil 比空 adjustment 更安全

观察型插件不需要制造一个“存在但为空”的 adjustment。返回 nil 清楚表达不修改，也减少 validator 和 merge 路径的歧义。

#### 7.4.4.2 日志要求

至少记录：

```text
plugin index/name
pod namespace/name/uid
sandbox ID
container ID/name
事件类型
调整前后关键资源
请求耗时
错误
```

但不要打印环境变量中的 secret、完整 mount credential 或 token。

---

### 7.4.5 NRI 插件的应用

#### 7.4.5.1 CPU/NUMA 绑定

插件可根据：

- Pod annotation；
- QoS；
- NUMA topology；
- 设备位置；
- 已分配 cpuset；

调整 Linux resources。需要与 kubelet CPU Manager、Topology Manager 协调，避免双方分别改 cpuset 导致漂移。

#### 7.4.5.2 设备注入

可为特定 workload 添加：

- `/dev` device；
- device cgroup rule；
- mount；
- env；
- annotation。

但 Kubernetes Device Plugin/CDI 已负责的设备不应被 NRI 重复注入。

#### 7.4.5.3 资源重平衡

NRI 支持返回对其他容器的 `ContainerUpdate`，可在节点负载变化时重分配 CPU/memory。风险是运行时与 kubelet期望资源可能不一致，因此必须定义：

```text
谁是期望状态源
哪些字段允许 NRI 改
更新是否持久
重启后如何 Synchronize
```

#### 7.4.5.4 安全验证

Default validator 可以约束 adjustment。生产应采用最小权限策略：

- 限制可注入的 host path；
- 限制 device；
- 禁止提高 privilege/capabilities；
- 限制 sysctl；
- 对插件二进制签名/校验；
- 锁定 plugin/config 目录权限。

#### 7.4.5.5 可用性策略

需要明确 fail-open 还是 fail-closed：

```text
策略/安全插件失败
  → 阻止容器启动？
  → 还是忽略调整继续？
```

containerd/NRI API 的错误通常会影响生命周期请求。插件设计者必须把外部依赖和慢操作移出同步关键路径。

---

## 7.5 一次请求如何穿过整个架构

以 `ctr run` 为例：

```text
ctr
  │ gRPC
  ├─ Images.Pull
  │   ├─ Resolver/Transfer
  │   ├─ Content Store
  │   ├─ Image metadata
  │   └─ Snapshotter unpack
  │
  ├─ Containers.Create
  │   └─ metadata DB 写 Container + OCI Spec
  │
  ├─ Tasks.Create
  │   ├─ Tasks service
  │   ├─ Runtime v2 TaskManager
  │   ├─ Bundle + rootfs mounts
  │   ├─ ShimManager.Start
  │   └─ shim → runc create
  │
  ├─ Tasks.Start
  │   └─ shim → runc start
  │
  ├─ Tasks.Wait
  │   └─ shim exit → event → client
  │
  ├─ Tasks.Delete
  └─ Containers.Delete + snapshot cleanup
```

以 Kubernetes Pod 为例，在前面再加：

```text
kubelet → CRI facade → CRI runtime/image service
```

并在 sandbox 创建路径加入：

```text
CNI + NRI + PodSandbox controller
```

---

## 7.6 源码阅读方法

### 7.6.1 从 API 入口向下读

例如 Task Create：

```bash
rg -n "CreateTask|Create\(ctx.*CreateTask|TaskManager.*Create" api plugins core client
```

推荐顺序：

```text
client NewTask
→ gRPC tasks service
→ internal task service
→ runtime TaskManager
→ ShimManager
→ shim task service
→ runc
```

#### 7.6.1.1 不要被同名函数迷惑

`Create` 可能同时存在于：

- protobuf server；
- service；
- runtime interface；
- TaskManager；
- shim service；
- runc wrapper。

先确认 receiver 类型和 package，再判断层次。

### 7.6.2 用 plugin URI 定位源码

看到日志：

```text
io.containerd.snapshotter.v1.overlayfs
```

搜索注册：

```bash
rg -n 'ID:.*"overlayfs"' plugins
```

再看 `Requires`、`Config` 和 `InitFn`，这三个字段基本解释了插件：

```text
依赖谁
怎么配置
启动时做什么
```

### 7.6.3 用事件验证调用链

终端一：

```bash
ctr events
```

终端二创建/启动/删除容器。把事件时间与 containerd debug 日志、shim 日志对齐，可验证调用顺序。

### 7.6.4 用进程和 socket 验证 shim

```bash
ps -ef | grep containerd-shim
find /run/containerd -type s -o -name 'shim.pid' -o -name 'address'
ctr tasks list
```

不要直接 kill 生产 shim 做实验。可在隔离节点创建测试容器后观察 daemon restart/reconnect。

---

## 7.7 常见故障的分层定位

| 现象 | 优先层次 |
|---|---|
| `ctr version` 连接失败 | daemon/socket/gRPC |
| `ctr plugins` 某插件 error | 插件配置/依赖/平台 |
| image record 有但 content 缺 | metadata/content/GC |
| snapshot prepare 失败 | snapshotter/backing filesystem |
| task create 失败，shim 未出现 | TaskManager/shim binary/bundle/mount |
| shim 出现但 runc create 失败 | OCI Spec/cgroup/namespace/security |
| containerd 重启后 task 消失 | shim state/reconnect/runtime |
| NRI 超时导致 Pod 创建慢 | NRI plugin/request timeout |
| CRI 失败但 Native API 正常 | CRI image/runtime/facade/CNI |

#### 7.7.1 `failed to start shim`

检查：

```bash
command -v containerd-shim-runc-v2
containerd-shim-runc-v2 -v
runc --version
journalctl -u containerd
```

可能原因：

- shim binary 不在 PATH；
- runtime_type 拼错；
- runtime_path 不存在；
- bundle/state 目录权限；
- socket 路径过长；
- shim 启动即崩溃；
- runtime options 无法解码。

#### 7.7.2 `failed to load existing shims`

关注：

- state/root 是否搬迁；
- shim address/pid 文件；
- namespace；
- runtime binary 版本；
- daemon 用户权限；
- 升级后 shim API compatibility。

#### 7.7.3 插件依赖循环或缺失

自研插件应避免：

```text
A Requires B
B Requires A
```

若使用 `GetSingle`，必须确认该类型确实唯一；snapshotter 这类多实例类型通常用 `GetByID`/`GetByType`。

---

## 7.8 与 containerd 1.7.1 参考书对照

| 原书内容 | containerd 2.2.5 更新 |
|---|---|
| Runtime v1 与 v2 并列 | 2.x 已移除 Runtime v1 实现，重点只读 Runtime v2 |
| shim 只管理单容器 | Runtime v2 支持按 sandbox/runtime 分组管理多个 task |
| daemon 重启不影响容器作为一句结论 | 具体机制是 shim 独立 + state + LoadExistingShims 重连 |
| API/Core/Backend 静态分层 | 还要理解插件依赖图、Transfer、Sandbox、MountManager |
| NRI 属于早期实验接口 | 2.2.5 集成 NRI v0.11.0，接口和 validator 更完整，但仍需谨慎治理 |
| CRI 是一个 gRPC plugin | 2.2.5 image/runtime/facade 拆分 |
| proxy plugin 只用于 snapshotter | 是通用外部后端适配机制，能力由 type adapter 决定 |

---

## 7.9 本章实验

### 实验一：画出本机插件图

```bash
ctr plugins list > /tmp/plugins.txt
containerd config dump > /tmp/config-effective.toml
```

挑选：

```text
metadata/bolt
overlayfs
runtime.v2/task
shim manager
cri
nri
```

从源码的 `Registration.Requires` 手工画依赖箭头。

### 实验二：观察 Container 与 Task 分离

```bash
ctr images pull docker.io/library/busybox:latest
ctr containers create docker.io/library/busybox:latest demo sleep 300
ctr containers list
ctr tasks list
```

此时只有 Container，无 Task。再：

```bash
ctr tasks start -d demo
ctr tasks list
ps -ef | grep containerd-shim
```

最后按 Task → Container 清理。

### 实验三：验证 daemon restart 与 shim

仅在实验节点：

```bash
ctr run -d docker.io/library/busybox:latest survive sleep 600
PID_BEFORE=$(ctr tasks list | awk '$1=="survive" {print $2}')
systemctl restart containerd
PID_AFTER=$(ctr tasks list | awk '$1=="survive" {print $2}')
echo "$PID_BEFORE $PID_AFTER"
```

观察 shim 进程和业务 PID 是否保持，阅读重连日志。

### 实验四：NRI observer

部署只观察插件，创建测试容器，确认事件顺序：

```text
Configure
Synchronize
CreateContainer
PostCreateContainer（若实现）
StartContainer（若实现）
PostStartContainer（若实现）
StopContainer
RemoveContainer
```

---

## 7.10 本章结论

1. containerd 由插件注册表和依赖图组织，不是单体硬编码架构。
2. gRPC API 是客户端入口，Service 做适配，Metadata/Content/Snapshot/Runtime 等 Backend 执行实际工作。
3. Container 是 metadata，Task 是运行实例；Tasks service 通过 Runtime v2 TaskManager 管理 shim。
4. shim 是 daemon 与 OCI/VM runtime 之间的常驻代理，支持 daemon 重启后的 reconnect。
5. Runtime v2 创建链包括 bundle、mount activation、shim start、runtime feature validation 和 shim Task.Create。
6. Proxy plugin 用于把后端能力放到外部进程，NRI 用于节点侧生命周期观察和资源调整。
7. 阅读源码时应从 API 入口向下，结合插件 URI、事件、进程和 socket 验证调用链。
