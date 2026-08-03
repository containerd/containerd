# 《containerd 原理剖析与实战》第 4 章伴读

> **适配版本：containerd 2.2.5**
> **对应原书章节：第 4 章 containerd 与云原生生态**
> **源码基线：用户提供的 `containerd-2.2.5` 源码包**

---

## 阅读说明

这一章最容易混乱的地方，是把 Kubernetes、CRI、containerd、runc 和 CNI 当成同一层。先建立总图：

```text
Kubernetes 控制面
        │
        │ PodSpec
        ▼
kubelet
        │
        │ CRI v1 gRPC
        ▼
containerd CRI 服务
        │
        ├── 镜像服务：拉取、查询、删除镜像
        ├── 运行时服务：PodSandbox、Container、Exec、Stats
        ├── Sandbox Controller
        ├── CNI
        └── NRI
        │
        │ containerd 内部 Service / Runtime v2 API
        ▼
containerd Core + shim + runc
        │
        ▼
Linux namespace / cgroup / mount / network
```

因此：

- Kubernetes 并不直接调用 `runc`。
- kubelet 也不直接调用 containerd Native API。
- kubelet 通过 CRI 调用 containerd 的 CRI 实现。
- CRI 再把 Kubernetes 语义翻译为 containerd 的镜像、容器、任务、沙箱和网络操作。

本文会把“原书中的概念”与“containerd 2.2.5 的真实插件结构”对应起来。外部项目如 Kubernetes、CRI-O、cri-dockerd 和 crictl 只解释其接口位置；具体实现以本源码包内可验证内容为边界。

---

## 4.1 Kubernetes 与 CRI

### 4.1.1 Kubernetes 概述

#### 4.1.1.1 Kubernetes 管理的不是一个孤立容器，而是 Pod

Docker 风格的思考通常是：

```text
运行一个容器
```

Kubernetes 的最小调度单位则是：

```text
Pod
├── 一个网络命名空间
├── 一个 IPC 命名空间（通常共享）
├── 一组卷
├── 一个或多个业务容器
└── 一个基础沙箱环境
```

这会直接影响 CRI 的对象模型。CRI 不只有 `CreateContainer`，还必须有：

```text
RunPodSandbox
StopPodSandbox
RemovePodSandbox
CreateContainer
StartContainer
StopContainer
RemoveContainer
```

`PodSandbox` 不是“多余的 pause 容器别名”，而是 CRI 用来承载 Pod 级资源和生命周期的抽象。Linux 默认实现中，经常由 pause 镜像中的常驻进程维持网络等命名空间；但在虚拟机型 Runtime、sandbox API 或远程 sandbox controller 下，沙箱未必等同于一个普通 pause 容器。

#### 4.1.1.2 kubelet 的职责边界

从容器运行时角度看，kubelet 主要负责：

1. 接收并维护节点上期望运行的 Pod。
2. 将 PodSpec 转换成 CRI 请求。
3. 轮询或订阅运行状态。
4. 执行探针、日志、exec/attach/port-forward 等节点侧管理。
5. 把资源配置、镜像信息、挂载和安全上下文交给运行时。

真正创建 namespace、配置 cgroup、挂载 rootfs 和执行容器进程的工作，最终落到 containerd、shim、runc 与 Linux 内核。

#### 4.1.1.3 Kubernetes 对 containerd 的典型调用链

以启动一个 Pod 为例：

```text
kubelet
  │
  ├─ RuntimeService.Version / Status
  │
  ├─ ImageService.ImageStatus
  │       └─ 不存在时 PullImage
  │
  ├─ RuntimeService.RunPodSandbox
  │       ├─ 创建 sandbox store 元数据和（非 hostNetwork 时）网络命名空间
  │       ├─ 调用 CNI 配置该网络命名空间
  │       ├─ 创建并启动 sandbox controller/task
  │       └─ 更新 sandbox 状态
  │
  ├─ RuntimeService.CreateContainer
  │       ├─ 生成 OCI Spec
  │       ├─ 创建 writable snapshot
  │       └─ 创建 containerd Container/Task
  │
  └─ RuntimeService.StartContainer
          └─ 启动业务进程
```

这里有一个非常重要的时序：

> `CreateContainer` 与 `StartContainer` 是两个阶段，与 containerd Native API 中 `NewTask` 和 `Task.Start` 分离的思想一致。

---

### 4.1.2 CRI 与 containerd 在 Kubernetes 生态中的演进

#### 4.1.2.1 为什么需要 CRI

没有 CRI 时，kubelet 若直接耦合某个容器引擎，会出现：

- kubelet 内部塞入特定运行时逻辑；
- 每增加一个运行时都要修改 Kubernetes；
- 镜像、容器、网络、日志、exec 的行为难以统一；
- Kubernetes 与运行时发布周期相互绑定。

CRI 的价值不是“又加一层”，而是把双方约束在稳定的 RPC 合同上：

```text
Kubernetes 只理解 CRI
运行时实现 CRI
```

#### 4.1.2.2 dockershim 时代与 containerd 时代的本质差别

历史上常见链路是：

```text
kubelet → dockershim → Docker Engine → containerd → runc
```

containerd 直接实现 CRI 后，链路变为：

```text
kubelet → containerd CRI → containerd Core → shim → runc
```

减少的不只是一个进程，还减少了 Docker Engine 的容器对象、网络和镜像管理语义转换。Kubernetes 所需的 PodSandbox、日志格式、CNI、RuntimeHandler 等能力直接由 CRI 层实现。

#### 4.1.2.3 containerd 2.2.5 的 CRI 插件拆分

这是阅读基于 1.7.1 的书时必须特别更新的部分。

在旧配置里，CRI 经常看起来像一个大插件：

```toml
[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "..."
  [plugins."io.containerd.grpc.v1.cri".containerd]
  [plugins."io.containerd.grpc.v1.cri".cni]
```

在 containerd 2.2.5 的配置版本 3 中，核心职责已拆成三个插件：

| 插件 URI | 插件类型与 ID | 职责 |
|---|---|---|
| `io.containerd.cri.v1.images` | `CRIServicePlugin/images` | 镜像、registry、snapshotter、解密与拉取配置 |
| `io.containerd.cri.v1.runtime` | `CRIServicePlugin/runtime` | Pod/Container 运行时、CNI、安全和 RuntimeHandler 配置 |
| `io.containerd.grpc.v1.cri` | `GRPCPlugin/cri` | 对外注册 CRI RuntimeService 与 ImageService，管理 streaming server |

源码位置：

- `plugins/cri/images/plugin.go`
- `plugins/cri/runtime/plugin.go`
- `plugins/cri/cri.go`

这不是简单改名。它体现出：

```text
配置与实现解耦
├── ImageService 独立
├── RuntimeService 独立
└── gRPC facade 独立
```

2.2.5 仍提供旧配置迁移逻辑。`containerd config migrate` 会把旧 `io.containerd.grpc.v1.cri` 下的字段迁移到新的 image/runtime/server 插件，但生产升级前仍应检查迁移结果，而不是只替换二进制。

#### 4.1.2.4 `k8s.io` namespace 的由来

CRI 插件创建 containerd 内部 client 时明确设置：

```go
containerd.WithDefaultNamespace(constants.K8sContainerdNamespace)
```

常量值为：

```text
k8s.io
```

因此 Kubernetes 创建的对象通常位于 containerd namespace `k8s.io`：

```bash
ctr -n k8s.io containers list
ctr -n k8s.io images list
ctr -n k8s.io tasks list
```

而直接运行：

```bash
ctr containers list
```

默认查询的往往是 `default` namespace，于是可能出现“kubelet 明明运行着 Pod，ctr 却什么也看不到”的错觉。

containerd namespace 只是元数据和资源命名空间，不是 Linux namespace。二者名称相同但层次完全不同。

---

### 4.1.3 CRI 概述

#### 4.1.3.1 CRI 是两组 gRPC 服务

containerd 2.2.5 vendored 的依赖为：

```text
k8s.io/cri-api v0.34.1
```

CRI v1 主要分成：

```text
RuntimeService
├── Version / Status / RuntimeConfig
├── Run/Stop/RemovePodSandbox
├── PodSandboxStatus / ListPodSandbox
├── Create/Start/Stop/RemoveContainer
├── ContainerStatus / ListContainers
├── Exec / ExecSync / Attach / PortForward
├── ContainerStats / PodSandboxStats
├── UpdateContainerResources
├── ReopenContainerLog
└── CheckpointContainer 等

ImageService
├── ListImages
├── ImageStatus
├── PullImage
├── RemoveImage
└── ImageFsInfo
```

`plugins/cri/cri.go` 的 `register()` 最终执行：

```go
runtime.RegisterRuntimeServiceServer(s, instrumented)
runtime.RegisterImageServiceServer(s, instrumented)
```

也就是说，kubelet 看到的是一台同时实现两组服务的 gRPC Server。

#### 4.1.3.2 CRI 请求如何落到 containerd 内部

CRI facade 初始化时不会通过外部 Unix Socket 再绕一圈，而是创建 in-memory client：

```go
containerd.New(
    "",
    containerd.WithDefaultNamespace("k8s.io"),
    containerd.WithInMemoryServices(ic),
)
```

这说明 CRI 与 containerd Core 位于同一 daemon 内时，可以直接使用已经初始化的 service，不必把每次内部调用重新序列化为外部 gRPC。

完整分层可画成：

```text
kubelet
  │ Unix socket: /run/containerd/containerd.sock
  ▼
GRPCPlugin/cri
  │
  ├── CRIServicePlugin/runtime
  ├── CRIServicePlugin/images
  ├── PodSandboxPlugin / SandboxControllerPlugin
  ├── NRIApiPlugin
  └── containerd in-memory client
          │
          ├── Images / Content / Snapshots
          ├── Containers / Tasks
          ├── Events / Leases
          └── Runtime v2
```

注意同一个 `/run/containerd/containerd.sock` 上既可以承载 containerd Native gRPC 服务，也注册了 CRI gRPC 服务；客户端通过不同 protobuf service 访问，不是靠端口区分。

#### 4.1.3.3 PodSandbox 的创建逻辑

`RunPodSandbox` 不是单一 `runc run`。其典型步骤包括：

1. 校验 sandbox 配置、预留名称、创建 lease 和 sandbox store 记录。
2. 选择 RuntimeHandler 与 sandbox controller；非 hostNetwork 时先创建网络命名空间并保存其路径。
3. 对该网络命名空间调用 CNI `ADD`，保存 CNI Result/Pod IP；失败时按逆序回滚。
4. controller 准备 pause 镜像和 snapshot，创建 containerd Container/Task 并启动 sandbox。
5. 保存 controller endpoint、labels 与 Ready 状态。

源码阅读入口：

- `internal/cri/server/sandbox_run.go`
- `internal/cri/server/podsandbox/`
- `internal/cri/server/service_linux.go`
- `internal/cri/server/cni_conf_syncer.go`

失败回滚尤其值得阅读：网络、task、snapshot 与 metadata 任一步失败，都可能需要逆序清理。容器运行时工程的复杂度，很多来自“半成功状态”而不是正常路径。

#### 4.1.3.4 Exec、Attach、PortForward 为什么不是普通 gRPC 流

CRI 的 `Exec`、`Attach`、`PortForward` 首先返回一个 URL，随后 kubelet 或客户端通过 streaming server 建立数据通道。原因是这些操作需要：

- 多路复用 stdin/stdout/stderr；
- 终端 resize；
- 长连接；
- 双向流；
- 与普通短 RPC 不同的超时和认证边界。

2.2.5 中 streaming 配置属于：

```toml
[plugins."io.containerd.grpc.v1.cri"]
```

而不再混在 runtime/image 插件下。

#### 4.1.3.5 CRI Status 与就绪性

CRI 插件初始化后调用：

```go
ready := ic.RegisterReadiness()
go s.Run(ready)
```

能成功进入 `Status` 本身已经说明 CRI gRPC 服务完成了初始化；在当前实现中 `RuntimeReady` 会直接返回 `true`。它会实际检查默认 CNI 的 `NetworkReady`，并额外报告未忽略的 containerd deprecation warnings。插件初始化失败、配置错误或服务根本未注册时，通常会在调用 `Status` 前表现为连接/服务错误或 `ctr plugins list` 的 `error`，而不是由 `RuntimeReady=false` 精细表达。

排障时不能只看：

```bash
systemctl is-active containerd
```

还应看：

```bash
crictl info
crictl version
journalctl -u containerd
ctr plugins list
```

一个 daemon 可以处于 active，但 CRI 插件因配置校验失败处于 error 状态。

---

### 4.1.4 几种 CRI 实现及其概述

#### 4.1.4.1 containerd CRI

特点：

- 与 containerd daemon 同进程插件化集成；
- 直接复用 content、image、snapshot、task、event、lease；
- 默认 Runtime v2 + runc；
- 支持 RuntimeHandler 选择不同 shim；
- 内置 CNI 调用层；
- 与 NRI、sandbox controller 等 containerd 能力衔接。

适合把 containerd 作为 Kubernetes 节点标准运行时。

#### 4.1.4.2 CRI-O

CRI-O 是面向 Kubernetes CRI 的独立运行时实现。它与 containerd 的共同点是都能接 kubelet、OCI runtime 与 CNI；差异在于内部对象模型、守护进程架构、存储实现与插件机制不同。

学习角度可这样理解：

```text
containerd：通用容器运行时平台 + CRI 插件
CRI-O：以 Kubernetes CRI 为中心的运行时
```

这不是简单的“谁更底层”，而是产品边界和内部架构不同。

#### 4.1.4.3 cri-dockerd

cri-dockerd 的意义是提供 CRI 到 Docker Engine 的适配桥。它适用于确实需要继续让 kubelet 管理 Docker Engine 的环境，但调用链会比 kubelet 直接接 containerd 更长。

```text
kubelet → cri-dockerd → Docker Engine → containerd → runc
```

不要把 `docker ps` 是否能看到 Kubernetes 容器，当作判断 Kubernetes 是否正常的标准。直接使用 containerd CRI 时，应该用 `crictl` 或 `ctr -n k8s.io`。

#### 4.1.4.4 虚拟机型 RuntimeHandler

CRI RuntimeHandler 可以把某类 Pod 交给不同 runtime：

```text
runc           普通 Linux 容器
Kata/VM shim   以轻量虚拟机提供更强隔离
其他 shim      特定硬件、机密计算或沙箱实现
```

containerd 的 CRI 配置用 `runtimes` map 表示多个 handler。Kubernetes 通过 RuntimeClass 把 handler 名传入 CRI。这里的 handler 名只是逻辑名称，真正执行什么由 `runtime_type`、`runtime_path`、options、snapshotter 和 sandboxer 决定。

---

## 4.2 containerd 与 CRI Plugin

### 4.2.1 containerd 中的 CRI Plugin

#### 4.2.1.1 插件依赖图

2.2.5 的 CRI facade 注册为：

```text
Type: io.containerd.grpc.v1
ID:   cri
```

它依赖：

```text
CRIServicePlugin
PodSandboxPlugin
SandboxControllerPlugin
NRIApiPlugin
EventPlugin
ServicePlugin
LeasePlugin
SandboxStorePlugin
TransferPlugin
WarningPlugin
```

其中它再按 ID 取出：

```text
CRIServicePlugin/runtime
CRIServicePlugin/images
```

这说明 containerd 的插件系统不是“按源码 import 顺序启动”，而是根据 `Requires` 构造依赖图后初始化。某个底层插件初始化失败时，依赖它的 CRI 插件也不能正常工作。

#### 4.2.1.2 Image Service 初始化

`plugins/cri/images/plugin.go` 的关键动作：

1. 取得 metadata DB。
2. 校验 registry/image 配置。
3. 取得 transfer service。
4. 创建 namespace 为 `k8s.io` 的 in-memory client。
5. 找到默认 snapshotter。
6. 建立 runtime 到 platform/snapshotter 的映射。
7. 创建 CRI ImageService。

它对 registry `config_path` 还有 Linux 默认行为：当 `config_path` 与旧 mirrors 都为空时，默认设置为：

```text
/etc/containerd/certs.d:/etc/docker/certs.d
```

这个冒号表示可搜索多个根目录，不是一个带冒号的目录名。

#### 4.2.1.3 Runtime Service 初始化

`plugins/cri/runtime/plugin.go` 负责：

- RuntimeConfig 校验；
- CNI 配置；
- runtime handler 映射；
- SELinux、AppArmor、seccomp 等安全配置；
- sandbox 模式；
- 容器日志与统计；
- RuntimeService 基础实现。

它是其他 CRI 运行时服务依赖的基础插件，但真正把 gRPC service 暴露给 kubelet的是 `plugins/cri/cri.go`。

#### 4.2.1.4 配置插件 URI 的读法

TOML 中：

```toml
[plugins."io.containerd.cri.v1.runtime"]
```

应拆成：

```text
插件类型：io.containerd.cri.v1
插件 ID：runtime
```

同理：

```toml
[plugins."io.containerd.grpc.v1.cri"]
```

是：

```text
插件类型：io.containerd.grpc.v1
插件 ID：cri
```

这与源码注册的 `Type` + `ID` 一一对应。理解这个规则后，`ctr plugins list` 的 TYPE、ID 和 config.toml 就能对上。

---

### 4.2.2 CRI Plugin 中的重要配置

#### 4.2.2.1 一份适合 Kubernetes 节点的最小骨架

```toml
version = 3

root = "/var/lib/containerd"
state = "/run/containerd"

[grpc]
  address = "/run/containerd/containerd.sock"

[plugins."io.containerd.cri.v1.images"]
  snapshotter = "overlayfs"

  [plugins."io.containerd.cri.v1.images".pinned_images]
    sandbox = "registry.k8s.io/pause:3.10.1"

  [plugins."io.containerd.cri.v1.images".registry]
    config_path = "/etc/containerd/certs.d:/etc/docker/certs.d"

[plugins."io.containerd.cri.v1.runtime"]
  enable_selinux = false
  max_container_log_line_size = 16384

  [plugins."io.containerd.cri.v1.runtime".cni]
    bin_dirs = ["/opt/cni/bin"]
    conf_dir = "/etc/cni/net.d"
    max_conf_num = 1
    setup_serially = false

  [plugins."io.containerd.cri.v1.runtime".containerd]
    default_runtime_name = "runc"

    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc]
      runtime_type = "io.containerd.runc.v2"

      [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc.options]
        SystemdCgroup = true

[plugins."io.containerd.grpc.v1.cri"]
  disable_tcp_service = true
  stream_server_address = "127.0.0.1"
```

不要把这段机械复制到所有环境。至少应确认：

- 主机是 cgroup v1 还是 v2；
- kubelet 使用 systemd 还是 cgroupfs driver；
- CNI 二进制和配置真实路径；
- 私有仓库证书、mirror 和认证方式；
- pause 镜像能否拉取；
- snapshotter 是否可用；
- SELinux/AppArmor 是否启用。

#### 4.2.2.2 `SystemdCgroup` 在哪里

它不直接属于 Runtime struct 的 TOML 字段，而是 runtime-specific `options`：

```toml
[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc.options]
  SystemdCgroup = true
```

原因是不同 shim/runtime 的 options protobuf 不同。CRI 先保存为通用 map，再由 runtime 类型解码成 runc options。

常见误区是写到：

```toml
[plugins."io.containerd.cri.v1.runtime"]
SystemdCgroup = true
```

这种位置不会产生期望效果。

#### 4.2.2.3 cgroup driver 一致性

Kubernetes 节点通常要求 kubelet 与 OCI runtime 对 cgroup 管理方式一致：

```text
kubelet: systemd
runc:    SystemdCgroup=true
```

不一致可能导致：

- Pod cgroup 层次不符合 kubelet 预期；
- 资源统计异常；
- 驱逐与 OOM 行为难以判断；
- cgroup v2 环境下层级管理冲突。

这里的 `SystemdCgroup` 不是“containerd 是否由 systemd 启动”，而是 runc 创建容器 cgroup 时是否通过 systemd manager 管理。

#### 4.2.2.4 RuntimeHandler 配置

```toml
[plugins."io.containerd.cri.v1.runtime".containerd]
  default_runtime_name = "runc"

[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  snapshotter = "devmapper"
  sandboxer = "shim"
```

映射关系：

```text
Kubernetes RuntimeClass.handler
        ↓
CRI RuntimeHandler 字符串
        ↓
containerd.runtimes.<name>
        ↓
runtime_type / runtime_path / options / snapshotter / sandboxer
```

`runtime_path` 可覆盖 shim 二进制解析，且源码要求绝对路径。通常优先使用规范 runtime type，让 containerd 按命名规则查找 shim；只有多版本并存或非标准位置时再指定 path。

#### 4.2.2.5 pause/sandbox 镜像

2.2.5 默认常量为：

```text
registry.k8s.io/pause:3.10.1
```

配置位置已经从旧版 `sandbox_image` 迁移为：

```toml
[plugins."io.containerd.cri.v1.images".pinned_images]
  sandbox = "registry.k8s.io/pause:3.10.1"
```

`pinned_images` 不只可放 sandbox，也允许其他由插件按 key 查找且不应被 CRI 客户端轻易删除的镜像。

#### 4.2.2.6 Registry 主机配置

2.2.5 推荐：

```toml
[plugins."io.containerd.cri.v1.images".registry]
  config_path = "/etc/containerd/certs.d"
```

目录结构示例：

```text
/etc/containerd/certs.d/
└── registry.example.com:5000/
    ├── hosts.toml
    ├── ca.crt
    ├── client.cert
    └── client.key
```

旧的 `mirrors`、`configs`、`auths` 字段在源码中已标记 deprecated，并写明计划在 containerd 2.3 移除。升级时应迁移到 hosts directory，而不是继续扩展旧内联配置。

#### 4.2.2.7 CNI 关键配置

2.2.5 Linux 默认：

| 字段 | 默认值 | 含义 |
|---|---|---|
| `bin_dirs` | `[/opt/cni/bin]` | 查找 CNI 可执行文件 |
| `conf_dir` | `/etc/cni/net.d` | 网络配置目录 |
| `max_conf_num` | `1` | 最多加载多少个配置；0 表示不限制 |
| `setup_serially` | `false` | 多网络配置是否串行执行 |
| `use_internal_loopback` | `false` | 是否由内部逻辑拉起 lo，而非 loopback CNI |

`bin_dir` 已废弃，推荐 `bin_dirs`。配置迁移逻辑会在适当情况下把旧单目录迁为数组。

`max_conf_num=1` 是历史兼容值，并不代表 CNI 只能有一个 plugin。一个 `.conflist` 内仍可包含多个 plugin；它限制的是 go-cni 从目录加载的顶层网络配置数量。

#### 4.2.2.8 Snapshotter 配置的两个层次

全局镜像默认：

```toml
[plugins."io.containerd.cri.v1.images"]
  snapshotter = "overlayfs"
```

某个 RuntimeHandler 覆盖：

```toml
[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata]
  snapshotter = "devmapper"
```

因此可以实现：

```text
runc Pod → overlayfs
kata Pod → devmapper
```

ImageService 还维护 runtime/platform/snapshotter 映射，以决定镜像按哪个平台解析和解包。

---

### 4.2.3 CRI Plugin 配置项全解

下面按 2.2.5 源码结构归类。字段的最终可用性还受平台、构建标签和对应 runtime 支持影响。

#### 4.2.3.1 `io.containerd.cri.v1.images`

| 字段 | 作用 | 阅读重点 |
|---|---|---|
| `snapshotter` | 默认镜像解包 snapshotter | 必须与已加载 snapshotter 插件匹配 |
| `disable_snapshot_annotations` | 禁止把镜像相关 annotation 传给 snapshotter | remote/lazy snapshotter 可能依赖这些 annotation |
| `discard_unpacked_layers` | 解包成功后允许 GC 清理 content 中层 blob | 节省空间，但重新分发/导出时可能要再拉取 |
| `pinned_images` | 插件固定使用的镜像 map | `sandbox` 替代旧 `sandbox_image` |
| `runtime_platforms` | runtime 到 platform/snapshotter 映射 | 多架构或不同 runtime 使用不同存储时关键 |
| `registry` | registry hosts、旧 mirrors/configs/auths | 推荐 `config_path` |
| `image_decryption` | 加密镜像解密模型 | 需要对应解密能力和密钥管理 |
| `max_concurrent_downloads` | 每次镜像拉取并发下载限制 | 使用 Transfer Service 时相关配置逐步转移到 transfer 插件 |
| `concurrent_layer_fetch_buffer` | 每次下载并发 chunk 缓冲限制 | 控制网络/内存并发 |
| `image_pull_progress_timeout` | 长时间无新字节时取消拉取 | 默认逻辑为 5 分钟，0 可表示不超时 |
| `image_pull_with_sync_fs` | 解包时强制同步文件系统的实验设置 | 完整性与性能权衡 |
| `stats_collect_period` | snapshot 统计采集周期，秒 | 影响 ImageFsInfo 等统计时效和开销 |
| `use_local_image_pull` | 使用本地 client.Pull，而非 Transfer Service | 默认 false，2.x 应重点理解 transfer 路径 |

#### 4.2.3.2 `io.containerd.cri.v1.runtime.containerd`

| 字段 | 作用 |
|---|---|
| `default_runtime_name` | 未指定 RuntimeHandler 时使用的 runtime 名 |
| `runtimes` | RuntimeHandler 到具体 Runtime 配置的 map |
| `ignore_blockio_not_enabled_errors` | 未启用 blockio 支持时忽略相关错误 |
| `ignore_rdt_not_enabled_errors` | 未启用 RDT 支持时忽略相关错误 |

每个 `runtimes.<name>` 的字段：

| 字段 | 作用 |
|---|---|
| `runtime_type` | shim runtime 类型，例如 `io.containerd.runc.v2` |
| `runtime_path` | 覆盖 shim 二进制路径，要求绝对路径 |
| `pod_annotations` | 允许传入 OCI Spec 的 Pod annotation 模式 |
| `container_annotations` | 允许传入 OCI Spec 的 Container annotation 模式 |
| `options` | runtime-specific options，例如 runc 的 `SystemdCgroup` |
| `privileged_without_host_devices` | privileged 容器不自动加入全部宿主设备 |
| `privileged_without_host_devices_all_devices_allowed` | 在上一选项启用时，是否仍允许全部设备规则 |
| `cgroup_writable` | 非 privileged 容器中是否提供可写 cgroup |
| `base_runtime_spec` | 以指定 JSON OCI Spec 作为所有容器基础模板 |
| `cni_conf_dir` | RuntimeHandler 专属 CNI 配置目录 |
| `cni_max_conf_num` | RuntimeHandler 专属 CNI 最大配置数 |
| `snapshotter` | RuntimeHandler 专属 snapshotter |
| `sandboxer` | `podsandbox` 或 `shim` 等 sandbox controller 模式 |
| `io_type` | `fifo` 或 `streaming` 等 IO 传输方式 |

`base_runtime_spec` 的作用不是替换 Kubernetes 的 ContainerConfig，而是作为 OCI Spec 基底，CRI 随后仍会应用镜像 config、资源、挂载、安全上下文和 annotation。模板内容不当可能对所有使用该 runtime 的容器产生影响。

#### 4.2.3.3 `io.containerd.cri.v1.runtime.cni`

| 字段 | 作用 |
|---|---|
| `bin_dir` | 旧单一二进制目录，已废弃 |
| `bin_dirs` | CNI 二进制搜索目录列表 |
| `conf_dir` | CNI 配置目录 |
| `max_conf_num` | 加载的顶层配置数量上限，0 为全部 |
| `setup_serially` | 多网络配置串行还是并行设置 |
| `conf_template` | 用 Go template 生成 CNI 配置的模板路径 |
| `ip_pref` | Pod IP 选择偏好：`ipv4`（或空值）、`ipv6`，或 `cni`（遵循 CNI 返回顺序） |
| `use_internal_loopback` | 使用内部方式配置 loopback |

#### 4.2.3.4 `io.containerd.cri.v1.runtime` 其他运行时字段

这些字段围绕安全、日志、统计与主机兼容性：

| 字段类别 | 典型字段 | 含义 |
|---|---|---|
| LSM | `enable_selinux`、`selinux_category_range`、`disable_apparmor` | SELinux/AppArmor 集成 |
| 日志 | `max_container_log_line_size` | CRI 日志单行拆分上限 |
| OOM | `restrict_oom_score_adj` | 限制容器 OOMScoreAdj 下界 |
| proc/seccomp | `disable_proc_mount`、`unset_seccomp_profile` | proc mount 与缺省 seccomp 行为 |
| 主机资源 | hugetlb、CDI、device ownership 等相关字段 | 把 CRI 设备与资源请求映射进 OCI Spec |
| 统计 | stats collection 相关字段 | Pod/Container stats 开销与精度 |
| sandbox | sandbox controller 与相关设置 | 决定 Pod 沙箱由何种 controller 承载 |

精确字段应以：

```text
internal/cri/config/config.go
internal/cri/config/config_unix.go
```

为准。建议用：

```bash
containerd config default
```

生成当前二进制的完整默认配置，再针对字段做最小修改，不应从旧博客复制整个 1.x 配置。

#### 4.2.3.5 `io.containerd.grpc.v1.cri`

server 配置主要负责 streaming 与 TCP 暴露：

| 字段 | 作用 |
|---|---|
| `disable_tcp_service` | 是否禁止在 containerd TCP gRPC listener 上注册 CRI |
| `stream_server_address` | exec/attach/port-forward streaming 监听地址 |
| `stream_server_port` | streaming 端口，0 通常表示动态选择 |
| `stream_idle_timeout` | streaming 空闲超时 |
| `enable_tls_streaming` | 是否启用 streaming TLS |
| `x509_key_pair_streaming` | streaming TLS 证书与私钥 |

生产环境不应为了方便而把 streaming 或 containerd gRPC 无认证暴露到公网。Unix Socket 权限、节点防火墙和 TLS 边界都应纳入威胁模型。

#### 4.2.3.6 配置升级方法

从 1.7 配置迁移到 2.2.5，建议：

```bash
containerd --config /etc/containerd/config.toml.old config migrate \
  > /etc/containerd/config.toml.new
```

然后执行：

```bash
containerd --config /etc/containerd/config.toml.new config dump
containerd --config /etc/containerd/config.toml.new
```

并重点比较：

```text
旧 io.containerd.grpc.v1.cri
  ├─ 镜像字段 → io.containerd.cri.v1.images
  ├─ runtime/CNI 字段 → io.containerd.cri.v1.runtime
  └─ streaming 字段 → io.containerd.grpc.v1.cri
```

不要只确认 TOML 能解析，还要看 `ctr plugins list` 中三个 CRI 相关插件是否为 `ok`。

---

## 4.3 crictl 的使用

### 4.3.1 crictl 概述

`crictl` 是 CRI 客户端，定位类似：

```text
ctr     → containerd Native API 调试
crictl  → CRI 调试
nerdctl → 面向通用容器使用体验
```

containerd 源码树不会实现完整 crictl 命令；本源码的测试脚本记录了一个 crictl 测试基线版本：

```text
script/setup/critools-version: v1.33.0
```

这不表示 containerd 2.2.5 只能搭配这一版本，而是源码测试环境的可追溯线索。

`crictl` 最适合回答：

- kubelet 通过 CRI 看到了什么；
- PodSandbox 是否 ready；
- 容器的 CRI 状态、标签和日志路径；
- PullImage 是否成功；
- CRI Status/Info 是否正常；
- exec/inspect/stats 是否工作。

它不适合查看 containerd 所有 content blob、lease、snapshot 或插件内部对象，那些属于 containerd Native API 视角。

---

### 4.3.2 crictl 的安装和配置

#### 4.3.2.1 Endpoint

常见配置文件：

```yaml
# /etc/crictl.yaml
runtime-endpoint: unix:///run/containerd/containerd.sock
image-endpoint: unix:///run/containerd/containerd.sock
timeout: 10
debug: false
pull-image-on-create: false
disable-pull-on-run: false
```

RuntimeService 和 ImageService 注册在同一 containerd socket 上，所以两个 endpoint 可以相同。

验证：

```bash
crictl version
crictl info
```

`crictl version` 通常同时显示客户端版本和运行时返回的 CRI 版本信息；`crictl info` 可查看 runtime config、CNI 状态和实现信息。

#### 4.3.2.2 Socket 权限

```bash
ls -l /run/containerd/containerd.sock
```

若普通用户无权限，不要直接把 Socket 改成全局可写。能访问该 Socket 的主体通常拥有创建高权限容器、挂载宿主目录等强大能力，应视为近似节点 root 权限。

#### 4.3.2.3 与 kubelet endpoint 对齐

排障时要确认 kubelet 实际使用的 endpoint 与 crictl 一致。否则可能出现：

```text
kubelet → /run/containerd/containerd.sock
crictl  → 另一个旧 socket
```

结果是两边看到完全不同的运行时状态。

---

### 4.3.3 crictl 使用说明

#### 4.3.3.1 查看 PodSandbox 与容器

```bash
crictl pods
crictl ps
crictl ps -a
```

对应关系：

```text
crictl pods  → CRI PodSandbox
crictl ps    → CRI Container
ctr -n k8s.io c ls → containerd Container metadata
ctr -n k8s.io t ls → containerd Task
```

同一个 Kubernetes 容器在不同视角下 ID、状态字段和展示形式可能不同，但底层会通过 labels 和 metadata 建立关联。

#### 4.3.3.2 Inspect

```bash
crictl inspectp <pod-id>
crictl inspect <container-id>
crictl inspecti <image-id-or-ref>
```

重点观察：

- labels 与 annotations；
- runtimeHandler；
- PID；
- logPath；
- mounts；
- Linux resources；
- network/IP；
- reason/message；
- imageRef 与 snapshot 信息。

对比 containerd：

```bash
ctr -n k8s.io containers info <id>
ctr -n k8s.io tasks info <id>
```

可以看到 CRI 如何把 Kubernetes 字段转成 containerd metadata 和 OCI Spec。

#### 4.3.3.3 镜像操作

```bash
crictl images
crictl pull registry.example.com/app:v1
crictl inspecti registry.example.com/app:v1
crictl rmi registry.example.com/app:v1
```

`crictl pull` 会走 CRI ImageService，因此使用的是 CRI image plugin 的 registry 配置。`ctr images pull` 走 Native API 客户端参数，两者在 hosts 配置、凭据来源、namespace 和 unpack 行为上可能不同。

所以：

> “ctr 能拉取”并不能百分之百证明“kubelet 通过 CRI 能拉取”。

更贴近 Kubernetes 路径的验证应使用 `crictl pull`。

#### 4.3.3.4 日志与 exec

```bash
crictl logs <container-id>
crictl exec -it <container-id> sh
```

日志读取遵循 CRI 日志路径与格式；exec 会先请求 CRI Exec 获得 streaming URL，再建立流通道。若容器正常但 exec 失败，应检查：

- streaming server 监听地址；
- 节点地址可达性；
- TLS 配置；
- 防火墙；
- idle timeout；
- 容器内命令是否存在。

#### 4.3.3.5 Stats

```bash
crictl stats
crictl statsp
```

数据来自 CRI RuntimeService 的 ContainerStats/PodSandboxStats，底层又可能读取 task cgroup、snapshot 使用量和网络统计。它与 Prometheus endpoint 的指标采集路径相关但不是同一个 API。

#### 4.3.3.6 手工运行 PodSandbox 和 Container

crictl 可以接收 JSON/YAML 配置：

```bash
POD_ID=$(crictl runp pod-config.json)
CONTAINER_ID=$(crictl create "$POD_ID" container-config.json pod-config.json)
crictl start "$CONTAINER_ID"
```

这里故意拆成三步，正好映射 CRI 生命周期：

```text
runp    → RunPodSandbox
create  → CreateContainer
start   → StartContainer
```

清理顺序：

```bash
crictl stop "$CONTAINER_ID"
crictl rm "$CONTAINER_ID"
crictl stopp "$POD_ID"
crictl rmp "$POD_ID"
```

先删业务容器，再删 PodSandbox，符合依赖逆序清理原则。

---

## 4.4 从源码排查 CRI 的方法

### 4.4.1 先看插件状态

```bash
ctr plugins list | grep -E 'cri|sandbox|runtime|snapshot|nri'
```

重点区分：

```text
ok       插件初始化成功
error    初始化失败
skip     条件不满足或依赖不可用
```

再看具体错误：

```bash
journalctl -u containerd -b --no-pager
```

### 4.4.2 看最终生效配置

```bash
containerd config dump
```

`config.toml` 是输入，`config dump` 更接近合并 imports、默认值和迁移后的视图。尤其要检查插件 URI 是否写对。

### 4.4.3 用三种客户端交叉验证

```text
crictl        验证 CRI
ctr           验证 containerd Native API
runc/shim 日志 验证低级运行时
```

典型判断：

| 现象 | 更可能的故障层 |
|---|---|
| `crictl info` 失败，`ctr version` 正常 | CRI 插件或配置 |
| `crictl pull` 失败，`ctr pull` 成功 | CRI registry/image 配置 |
| sandbox 创建失败且 CNI not ready | CNI 配置/二进制/网络插件 |
| Container created 但 start 失败 | shim/runc/OCI Spec/安全策略 |
| containerd active 但插件 error | 插件依赖或配置校验 |

### 4.4.4 找到请求入口再沿调用链读

以 `RunPodSandbox` 为例，推荐顺序：

```text
CRI protobuf RuntimeService
  ↓
plugins/cri/cri.go                  注册服务
  ↓
internal/cri/instrument             监控包装
  ↓
internal/cri/server                 CRI service
  ↓
internal/cri/server/sandbox_run.go  业务流程
  ↓
containerd client/service
  ↓
core/runtime/v2
  ↓
shim/runc
```

不要从 `runc` 反向搜索所有代码；先确定 API 入口和对象 ID，阅读效率高得多。

---

## 4.5 与 containerd 1.7.1 参考书对照

| 原书中可能看到的内容 | containerd 2.2.5 应更新为 |
|---|---|
| CRI 是单个 `io.containerd.grpc.v1.cri` 大配置块 | image/runtime/server 三插件拆分 |
| `sandbox_image` | `io.containerd.cri.v1.images.pinned_images.sandbox` |
| CNI 配置在旧 CRI 大块中 | `io.containerd.cri.v1.runtime.cni` |
| runtime 配置在旧 CRI 大块中 | `io.containerd.cri.v1.runtime.containerd.runtimes` |
| registry mirrors/configs 内联配置 | 推荐 registry `config_path`/hosts directory |
| `bin_dir` | 推荐 `bin_dirs` |
| 仅理解 pause 容器 | 同时理解 PodSandbox、sandbox controller 与 shim sandbox |
| 只会用 `ctr` 看 Kubernetes 容器 | 优先 `crictl`，必要时 `ctr -n k8s.io` 下钻 |

---

## 4.6 本章实验

### 实验一：确认三个 CRI 插件

```bash
containerd config default | grep -nE 'io.containerd.(cri.v1|grpc.v1.cri)'
ctr plugins list | grep cri
```

目标：看到 image、runtime、gRPC facade 的分工，而不是把 CRI 当成一个黑盒。

### 实验二：比较 Native API 与 CRI 视图

```bash
crictl pods
crictl ps -a
ctr -n k8s.io containers list
ctr -n k8s.io tasks list
```

选择同一容器：

```bash
crictl inspect <id> > /tmp/cri.json
ctr -n k8s.io containers info <id> > /tmp/containerd.json
```

比较 labels、annotations、runtime 和 snapshot key。

### 实验三：验证 CRI 镜像链路

```bash
crictl pull docker.io/library/busybox:latest
crictl images | grep busybox
ctr -n k8s.io images list | grep busybox
```

目标：理解 CRI pull 后镜像进入 `k8s.io` namespace。

### 实验四：配置错误定位

在实验环境把 CNI `conf_dir` 改成不存在路径，重启后观察：

```bash
crictl info
ctr plugins list | grep cri
journalctl -u containerd -b
```

恢复配置后再次验证。生产节点不要直接做破坏性实验。

---

## 4.7 本章结论

1. CRI 是 kubelet 与容器运行时之间的 gRPC 合同，不等于 containerd Native API。
2. Kubernetes 以 PodSandbox + Container 为核心，不能只用“运行一个容器”的模型理解。
3. containerd 2.2.5 将 CRI 拆为 image、runtime、gRPC facade 三个插件。
4. Kubernetes 对象默认进入 containerd 的 `k8s.io` namespace。
5. CRI 内部通过 in-memory client 复用 containerd Core 服务，再进入 Runtime v2、shim 和 runc。
6. `crictl` 最贴近 kubelet 视角；`ctr` 用于向下检查 containerd 原生对象。
7. 从 1.7 升级到 2.2.5，重点检查配置版本 3、CRI 插件 URI、pinned images、registry hosts 和 CNI `bin_dirs`。
