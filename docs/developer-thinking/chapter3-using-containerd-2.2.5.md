# 《containerd 原理剖析与实战》第 3 章伴读

> **适配版本：containerd 2.2.5**
> **对应原书章节：第 3 章 使用 containerd**
> **源码基线：用户提供的 `containerd-2.2.5` 源码包**

---

## 阅读说明

这一章的目标不是背诵 `ctr`、`nerdctl` 命令，而是建立三个层次的操作模型：

```text
systemd / containerd 配置
        │
        ▼
containerd daemon
        │
        ├── Native API 客户端：ctr、nerdctl、Go client
        │
        └── CRI 客户端：kubelet、crictl
```

需要先记住：

1. `containerd` 是守护进程。
2. `ctr` 是随 containerd 源码一起构建的调试与管理客户端。
3. `nerdctl` 是独立项目，不在 containerd 2.2.5 源码树中。
4. `crictl` 调用的是 CRI，而不是 containerd Native API。
5. `Container` 与 `Task` 是两个对象；`ctr containers` 和 `ctr tasks` 因此也必须分开理解。

本文中：

- **源码事实**：可在 containerd 2.2.5 源码中直接定位。
- **操作建议**：适合实验或生产部署，但不代表 containerd API 的稳定承诺。
- **边界说明**：nerdctl、BuildKit、CNI 插件和 crictl 属于独立项目，本文只从 containerd 2.2.5 的接口边界解释它们。

---

## 3.1 containerd 的安装与部署

### 3.1.1 containerd 的安装

#### 3.1.1.1 安装 containerd 实际是在安装什么

一套可运行的 Linux containerd 环境通常至少包含：

```text
containerd
├── containerd                 daemon
├── ctr                        Native API 调试客户端
├── containerd-shim-runc-v2    默认 Linux Runtime v2 shim
├── runc                       OCI 低级运行时，通常单独安装
├── CNI plugin binaries        Kubernetes/nerdctl 网络所需，通常单独安装
├── config.toml                daemon 配置
└── containerd.service         systemd unit
```

containerd 官方源码文档明确区分了三步：

1. 安装 containerd 二进制。
2. 安装 runc。
3. 安装 CNI 插件。

这意味着：

> 安装了 `containerd` 二进制，不等于已经具备 Kubernetes Pod 网络，也不等于一定能找到 `runc`。

**源码定位：**

- `docs/getting-started.md:3-106`
- `Makefile:87-88`
- `cmd/containerd-shim-runc-v2/main.go`
- `defaults/defaults_linux.go:19-37`

#### 3.1.1.2 containerd 2.2.5 的默认路径

Linux 构建中的默认值如下：

| 用途 | 默认值 | 源码 |
|---|---|---|
| 主配置目录 | `/etc/containerd` | `defaults/defaults_unix.go:22-29` |
| 主配置文件 | `/etc/containerd/config.toml` | `cmd/containerd/command/main.go` |
| 配置片段 | `/etc/containerd/conf.d/*.toml` | `defaults/defaults_unix.go:28-29` |
| 持久化根目录 | `/var/lib/containerd` | `defaults/defaults_unix.go:24-26` |
| 临时状态目录 | `/run/containerd` | `defaults/defaults_linux.go:33-35` |
| gRPC Unix Socket | `/run/containerd/containerd.sock` | `defaults/defaults_linux.go:20-21` |
| 调试 socket 常量 | `/run/containerd/debug.sock` | `defaults/defaults_linux.go:22-23` |
| 默认 Runtime | `io.containerd.runc.v2` | `defaults/defaults_linux.go:27-28` |
| 默认 Snapshotter | `overlayfs` | `defaults/defaults_linux.go:29-32` |

理解 `root` 与 `state` 的区别：

```text
/var/lib/containerd       root：持久化
├── content blobs
├── meta.db
├── snapshotter 数据
└── 插件持久化数据

/run/containerd           state：易失状态
├── containerd.sock
├── shim bundle / socket / pid
├── FIFO
└── 重启后可以重新建立的运行时状态
```

不能把 `/run/containerd` 当成持久化备份目录，也不应在 containerd 运行期间随意删除。

`DefaultDebugAddress` 只是代码中的默认常量；生成的默认配置中 `[debug].address` 为空，因此不会自动监听 `/run/containerd/debug.sock`。只有显式配置该地址（或使用相应启动参数）后，debug/pprof listener 才会创建。不要把表中的常量误当成默认已开放的 socket。

#### 3.1.1.3 从二进制包安装

典型安装逻辑：

```bash
tar -C /usr/local -xzf containerd-2.2.5-linux-amd64.tar.gz

install -m 0755 runc.amd64 /usr/local/sbin/runc

mkdir -p /opt/cni/bin
tar -C /opt/cni/bin -xzf cni-plugins-linux-amd64-<version>.tgz

mkdir -p /etc/containerd
containerd config default > /etc/containerd/config.toml
```

安装后先检查：

```bash
containerd --version
ctr version
runc --version

command -v containerd
command -v containerd-shim-runc-v2
command -v runc
ls -l /opt/cni/bin
```

`containerd-shim-runc-v2` 的命名不是随意约定。Runtime 名：

```text
io.containerd.runc.v2
```

在 Unix 平台会被转换为：

```text
containerd-shim-runc-v2
```

映射规则由 `pkg/shim/util_unix.go` 中的 `shimBinaryFormat` 和 `BinaryName()` 一类逻辑完成。

#### 3.1.1.4 从源码构建

`go.mod` 声明：

```text
module github.com/containerd/containerd/v2
go 1.25.0
```

所以读取或修改 2.2.5 源码时，优先使用与 `go.mod` 一致的 Go 工具链。典型构建：

```bash
make binaries
sudo make install
```

源码中的核心命令集合由 Makefile 定义：

```text
ctr
containerd
containerd-stress
```

`containerd-shim-runc-v2` 也由项目构建流程产生。

生产环境更建议使用经过发行方测试、签名和打包的发行介质，而不是在目标节点临时执行 `go build`。源码构建更适合：

- 阅读和打断点；
- 修改插件；
- 插入日志；
- 验证修复；
- 构建内部定制版本。

#### 3.1.1.5 生成 containerd 2.2.5 配置

执行：

```bash
containerd config default > /etc/containerd/config.toml
```

不是把一份硬编码模板简单打印出来。源码流程是：

```text
containerd config default
  → platformAgnosticDefaultConfig()
  → LoadPlugins()
  → 读取每个插件注册时提供的 Config 默认对象
  → 汇总插件默认配置
  → 将 version 设置为当前最高版本
  → TOML 编码输出
```

关键代码：

- `cmd/containerd/command/config.go:36-75`
- `cmd/containerd/command/config.go:128-142`
- `version/version.go:37-41`

containerd 2.2.5 的最高配置版本是：

```toml
version = 3
```

原书若大量使用：

```toml
version = 2
[plugins."io.containerd.grpc.v1.cri"]
```

阅读时必须注意：2.2.5 会迁移旧配置，但新生成配置中的 CRI 已拆分到新的插件配置段，详见第 4 章。

#### 3.1.1.6 配置文件的三个命令

```bash
containerd config default
containerd config dump
containerd config migrate
```

含义：

| 命令 | 含义 |
|---|---|
| `default` | 输出当前二进制中所有已注册插件的默认配置 |
| `dump` | 加载指定主配置及其 imports、执行迁移后，补齐已注册插件的默认值并输出 |
| `migrate` | 对指定主配置执行与 `dump` 相同的迁移并输出；当前实现不读取一个单独的 `--input` 参数 |

特别适合审查“我明明配置了，解析和合并后是什么”：

```bash
containerd config dump > /tmp/effective-config.toml
```

因为 2.2.5 默认支持：

```toml
imports = ["/etc/containerd/conf.d/*.toml"]
```

后加载的配置片段可能覆盖主配置。它不是运行中每个插件的动态状态导出：启动参数、环境、内核能力和外部插件状态仍要结合实际 daemon 日志及 `ctr plugins list` 判断。

---

### 3.1.2 配置 containerd.service

containerd 2.2.5 源码根目录自带 `containerd.service`：

```ini
[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target dbus.service

[Service]
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/containerd

Type=notify
Delegate=yes
KillMode=process
Restart=always
RestartSec=5

LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
```

**源码定位：** `containerd.service:15-41`

#### 3.1.2.1 `ExecStartPre=-/sbin/modprobe overlay`

作用是尝试加载 OverlayFS 内核模块。

前面的 `-` 表示即使命令失败，systemd 也继续启动服务。原因包括：

- OverlayFS 可能已经编进内核，不需要加载模块；
- 系统可能选用其他 snapshotter；
- 某些精简系统没有 `/sbin/modprobe`。

不要把它理解为“containerd 只能使用 overlayfs”。

#### 3.1.2.2 `ExecStart=/usr/local/bin/containerd`

这与官方二进制包解压到 `/usr/local` 的路径一致。发行版包可能使用：

```text
/usr/bin/containerd
```

安装时必须确保 unit 中路径与实际二进制一致：

```bash
systemctl cat containerd
readlink -f "$(command -v containerd)"
```

#### 3.1.2.3 `Type=notify`

containerd 启动完成后向 systemd 发送 readiness 通知，而不是仅凭进程 fork 成功就认为服务可用。

containerd 内部还维护插件 readiness：

```text
daemon 启动
  → 插件按依赖顺序初始化
  → 需要异步就绪的插件注册 readiness
  → 所有关键 readiness 完成
  → 通知 systemd READY
```

因此 `systemctl start containerd` 返回成功，通常比“进程已经存在”更有意义。

#### 3.1.2.4 `Delegate=yes`

这是容器运行时最重要的 systemd 配置之一。

systemd 默认认为服务 cgroup 由自己管理。containerd 和 runc 又需要在其下创建和管理容器 cgroup。`Delegate=yes` 表示将子 cgroup 管理权委托给服务。

没有委托时可能出现：

- 无法创建子 cgroup；
- controller 未委托；
- systemd 与 runtime 同时修改 cgroup；
- 容器资源限制或统计异常；
- cgroup v2 环境问题尤其明显。

#### 3.1.2.5 `KillMode=process`

containerd 的设计依赖 shim 与 daemon 生命周期解耦。

```text
containerd
   ├── shim A ── container A
   └── shim B ── container B
```

当 containerd daemon 重启时，运行中的 shim 和容器不应被 systemd 一并杀死。`KillMode=process` 让 systemd 只终止主进程，而不是杀死该 unit cgroup 中所有进程。

这正是“containerd 可以重启并重新连接已有 shim”的运维基础之一。

#### 3.1.2.6 `Restart=always`

daemon 异常退出后自动重启。重启后的 containerd 会扫描 Runtime v2 状态目录并加载已有 shim：

```text
containerd restart
  → Runtime v2 TaskManager 初始化
  → ShimManager.LoadExistingShims()
  → 读取 bundle/bootstrap 信息
  → 重新连接 shim
  → 恢复 Task 管理视图
```

源码定位：

- `core/runtime/v2/task_manager.go:81-103`
- `core/runtime/v2/shim_load.go:36-202`

#### 3.1.2.7 `OOMScoreAdjust=-999`

降低 containerd daemon 被 OOM killer 选中的概率，但并不是绝对不会被杀。shim 和容器进程还会根据各自策略调整 OOM 分数。

这项配置保护的是控制面守护进程，并不替代：

- 节点内存预留；
- kubelet eviction；
- 容器 memory limit；
- 系统级 OOM 监控。

#### 3.1.2.8 推荐部署流程

```bash
install -D -m 0644 containerd.service \
  /usr/local/lib/systemd/system/containerd.service

mkdir -p /etc/containerd
containerd config default > /etc/containerd/config.toml

systemctl daemon-reload
systemctl enable --now containerd
systemctl status containerd --no-pager
```

验证：

```bash
journalctl -u containerd -b --no-pager
ctr version
ctr plugins ls
ss -lx | grep containerd.sock
```

重点观察 `ctr plugins ls` 的 `STATUS`：

- `ok`：初始化成功；
- `skip`：当前平台或条件不满足，插件主动跳过；
- `error`：插件初始化失败。

#### 3.1.2.9 配置修改后的正确动作

```bash
containerd config dump >/tmp/effective.toml
containerd --config /etc/containerd/config.toml config dump >/tmp/check.toml
systemctl restart containerd
journalctl -u containerd -n 200 --no-pager
```

不要假设所有配置都支持热更新。daemon 配置和大多数插件配置需要重启 containerd。

---

## 3.2 ctr 的使用

### 3.2.1 ctr 的安装与定位

`ctr` 与 containerd 同仓库、同版本构建。入口位于：

- `cmd/ctr/main.go`
- `cmd/ctr/app/main.go`

2.2.5 源码直接在帮助描述中声明：

> ctr 是一个不受稳定性承诺支持的调试和管理客户端，命令、选项和行为可能随版本变化。

源码定位：`cmd/ctr/app/main.go:70-89`

因此：

- 可以用 `ctr` 学习 containerd 对象模型；
- 可以用 `ctr` 排查底层镜像、content、snapshot、task；
- 不建议让业务系统解析 `ctr` 输出并依赖其 CLI 稳定性；
- 正式集成应使用 Go client 或 gRPC API。

全局参数：

```bash
ctr \
  --address /run/containerd/containerd.sock \
  --namespace default \
  --timeout 30s \
  <command>
```

对应环境变量：

```bash
CONTAINERD_ADDRESS=/run/containerd/containerd.sock
CONTAINERD_NAMESPACE=k8s.io
CONTAINERD_SNAPSHOTTER=overlayfs
```

#### 3.2.1.1 ctr 2.2.5 的一级命令

源码注册了：

```text
plugins
version
containers
content
events
images
leases
namespaces
pprof
run
snapshots
tasks
install
oci
sandboxes
info
deprecations
```

源码定位：`cmd/ctr/app/main.go:119-137`

建议按对象层次记忆：

```text
镜像分发层：content / images
文件系统层：snapshots
元数据层：containers / namespaces / leases
运行层：tasks / run / sandboxes
系统观察：plugins / events / info / pprof / deprecations
```

---

### 3.2.2 namespace

#### 3.2.2.1 containerd namespace 不是 Linux namespace

containerd namespace 是 daemon 内部的多租户元数据隔离键。它隔离：

- images 记录；
- containers；
- tasks；
- snapshots 元数据；
- leases；
- events 视图。

它不直接创建 PID、Network、Mount namespace。

默认 `ctr` namespace 来自：

```go
namespaces.Default
```

通常为：

```text
default
```

Kubernetes CRI 固定使用：

```text
k8s.io
```

CRI 初始化客户端时明确配置：

```go
containerd.WithDefaultNamespace(constants.K8sContainerdNamespace)
```

源码定位：`plugins/cri/cri.go:96-102`

#### 3.2.2.2 常用命令

```bash
ctr namespaces list
ctr namespaces create demo
ctr namespaces label demo owner=team-a
ctr namespaces remove demo
```

别名：

```bash
ctr ns ls
ctr ns create demo
```

指定 namespace：

```bash
ctr -n demo images ls
ctr -n k8s.io containers ls
```

`ctr namespaces create` 的源码调用链：

```text
ctr namespaces create demo
  → commands.NewClient()
  → client.NamespaceService()
  → NamespaceService.Create(ctx, "demo", labels)
  → gRPC Namespaces service
  → metadata namespace store
```

源码定位：

- `cmd/ctr/commands/namespaces/namespaces.go:46-64`
- `plugins/services/namespaces/service.go`
- `plugins/services/namespaces/local.go`
- `core/metadata/namespaces.go`

#### 3.2.2.3 为什么在 `ctr images ls` 看不到 Kubernetes 镜像

因为你默认查的是 `default`：

```bash
ctr images ls
```

而 kubelet/CRI 使用 `k8s.io`：

```bash
ctr -n k8s.io images ls
```

反过来，若你在默认 namespace 拉取镜像：

```bash
ctr images pull docker.io/library/busybox:latest
```

CRI 不一定能看到对应的 image metadata。导入 Kubernetes 使用的镜像应明确：

```bash
ctr -n k8s.io images import image.tar
```

注意：底层 content blob 在默认 `shared` 策略下可能跨 namespace 复用，但 image 元数据仍属于 namespace。这正是“磁盘上已经有 blob，但另一个 namespace 仍需要 image record”的原因。

---

### 3.2.3 镜像操作

#### 3.2.3.1 Image 与 Content 的区别

```text
Image record
  └── target descriptor
        ├── index/manifest blob
        ├── config blob
        └── layer blobs

Content Store
  └── 按 digest 保存真实不可变 blob

Snapshotter
  └── 将 layer blobs 解包为可挂载文件系统快照
```

所以：

```bash
ctr images ls
ctr content ls
ctr snapshots ls
```

看到的是三个不同层次。

#### 3.2.3.2 拉取镜像

```bash
ctr images pull docker.io/library/busybox:latest
```

常见选项：

```bash
ctr images pull \
  --platform linux/amd64 \
  --snapshotter overlayfs \
  --hosts-dir /etc/containerd/certs.d \
  docker.io/library/busybox:latest
```

简化调用链：

```text
ctr images pull
  → 创建 containerd Client
  → 配置 registry resolver/hosts
  → 请求 manifest/index
  → 下载 config 和 layer descriptors
  → blob 写入 Content Store
  → 创建/更新 Image metadata
  → 对目标平台执行 unpack
  → Snapshotter Prepare
  → Diff Apply 解压 layer
  → Commit 为 ChainID 快照
```

2.2.5 中镜像传输既存在 Transfer Service 路径，也保留本地拉取兼容路径。理解时不要再把“pull”视作单个 HTTP 下载动作。

#### 3.2.3.3 镜像名称必须尽量完整

推荐：

```text
docker.io/library/busybox:latest
registry.example.com/team/app:v1
```

而不是只写：

```text
busybox
```

因为 containerd Native API 不承诺完全复制 Docker CLI 的短名补全体验。nerdctl 会提供更接近 Docker 的用户体验，但 `ctr` 更强调底层明确性。

#### 3.2.3.4 常用镜像命令

```bash
ctr images ls
ctr images inspect docker.io/library/busybox:latest
ctr images tag SOURCE TARGET
ctr images remove IMAGE
ctr images export image.tar IMAGE
ctr images import image.tar
ctr images push registry.example.com/team/app:v1
ctr images mount IMAGE /mnt/image
ctr images unmount /mnt/image
ctr images usage
```

理解 `remove`：

- 删除 image metadata 不一定立即删除所有 blob；
- blob 是否可回收由 GC 引用关系、lease、snapshot、其他 image 引用决定；
- 已解包快照也不是简单随 image record 同步删除。

#### 3.2.3.5 私有仓库配置

临时命令参数：

```bash
ctr images pull \
  --user username:password \
  registry.example.com/team/app:v1
```

长期配置推荐 hosts 目录：

```text
/etc/containerd/certs.d/<registry>/hosts.toml
```

调用时：

```bash
ctr images pull \
  --hosts-dir /etc/containerd/certs.d \
  registry.example.com/team/app:v1
```

`ctr` 的 RegistryFlags 在源码中包括：

- `--skip-verify`
- `--plain-http`
- `--user`
- `--hosts-dir`
- `--tlscacert`
- `--tlscert`
- `--tlskey`
- `--http-dump`
- `--http-trace`

源码定位：`cmd/ctr/commands/commands.go:54-99`

生产环境不要长期依赖 `--skip-verify`。

---

### 3.2.4 容器操作

#### 3.2.4.1 `containers` 与 `tasks` 必须分开

```text
Container
  = 持久化元数据
  = OCI Spec + Image + Snapshot + Runtime + labels

Task
  = 正在运行或已创建的执行实例
  = shim + runtime + Linux process
```

因此：

```bash
ctr containers ls
```

不等于：

```bash
ctr tasks ls
```

一种典型状态：

```text
Container 存在
Task 不存在
```

表示元数据和 rootfs 可能仍在，但当前没有运行进程。

#### 3.2.4.2 `ctr run` 是组合命令

```bash
ctr run --rm -t \
  docker.io/library/busybox:latest \
  demo \
  sh
```

逻辑上组合了：

```text
检查/读取 Image
  → NewContainer
      → WithImage
      → WithNewSnapshot
      → WithNewSpec
  → container.NewTask
  → task.Start
  → wait
  → 清理 Task
  → --rm 时清理 Container/Snapshot
```

这就是为什么 `ctr run` 用起来像 Docker `run`，但底层仍然遵守 Container 与 Task 分离模型。

#### 3.2.4.3 分步创建更适合学习

先创建 Container：

```bash
ctr containers create \
  docker.io/library/busybox:latest \
  demo
```

查看：

```bash
ctr containers info demo
ctr containers ls
```

创建并启动 Task：

```bash
ctr tasks start -d demo
ctr tasks ls
ctr tasks ps demo
```

进入容器执行进程：

```bash
ctr tasks exec \
  --exec-id shell \
  -t demo sh
```

停止和删除：

```bash
ctr tasks kill --signal SIGTERM demo
ctr tasks delete demo
ctr containers delete demo
```

实际命令选项应以当前 `ctr --help` 为准，因为源码明确不保证 CLI 向后兼容。

#### 3.2.4.4 `create` 与 `start` 的语义

```text
NewTask / runc create
  → namespace、cgroup、rootfs 等已准备
  → init process 处于 created 阶段
  → 用户程序还没有正常开始执行

Task.Start / runc start
  → 释放 create 阶段同步点
  → exec 用户程序
```

这使 containerd 能在“进程已经被创建”和“工作负载真正开始”之间执行必要操作。

#### 3.2.4.5 常用 Task 命令

```bash
ctr tasks ls
ctr tasks start
ctr tasks attach
ctr tasks exec
ctr tasks ps
ctr tasks metrics
ctr tasks pause
ctr tasks resume
ctr tasks kill
ctr tasks delete
```

源码目录：

```text
cmd/ctr/commands/tasks/
```

每个子命令最终通过 Tasks gRPC Service 调用 `plugins/services/tasks`，后者再调用 Runtime v2 TaskManager 和 shim。

#### 3.2.4.6 一条完整的排障链

```bash
# 1. daemon 是否可达
ctr version

# 2. 插件是否正常
ctr plugins ls

# 3. namespace 是否正确
ctr namespaces ls

# 4. image metadata
ctr -n k8s.io images ls

# 5. content 是否存在
ctr -n k8s.io content ls

# 6. snapshot 是否解包
ctr -n k8s.io snapshots ls

# 7. container metadata
ctr -n k8s.io containers ls

# 8. task 是否存在
ctr -n k8s.io tasks ls

# 9. task PID
ctr -n k8s.io tasks ps <container-id>

# 10. 事件
ctr -n k8s.io events
```

这比只看 `docker ps` 式的单一视图更接近 containerd 的真实分层。

---

## 3.3 nerdctl 的使用

### 3.3.1 nerdctl 的设计初衷

containerd 2.2.5 源码文档将几个客户端区分为：

| 工具 | API | 面向对象 |
|---|---|---|
| `ctr` | containerd Native API | 调试、底层管理 |
| `nerdctl` | containerd Native API | 通用、友好的容器 CLI |
| `crictl` | CRI | Kubernetes 节点调试 |

源码定位：`docs/getting-started.md:157-179`

nerdctl 解决的核心问题是：

> containerd 已经有完整能力，但 `ctr` 有意保持底层、调试导向，也不承诺 Docker 风格的稳定体验。

nerdctl 通常补齐：

- Docker 风格命令；
- 自动 CNI 网络；
- 端口映射；
- volume；
- BuildKit 构建；
- Compose；
- rootless 体验；
- 更符合日常用户习惯的输出。

#### 3.3.1.1 源码边界

nerdctl 不在本次上传的 containerd 2.2.5 源码中。因此本文能够从 containerd 源码确认的是：

- 它连接 containerd Native API；
- containerd 提供 image/content/snapshot/container/task 等服务；
- containerd 本身不会因客户端是 nerdctl 就改变核心对象模型。

本文不能仅凭 containerd 源码证明某个 nerdctl 版本的内部实现细节。阅读 nerdctl 源码时应使用与实际安装版本一致的 nerdctl 仓库。

---

### 3.3.2 安装和部署 nerdctl

nerdctl 常见发行方式分为：

```text
minimal
  └── nerdctl CLI

full
  ├── nerdctl
  ├── containerd
  ├── runc
  ├── CNI plugins
  ├── BuildKit
  └── rootless 相关组件
```

具体包内容以所使用的 nerdctl 发行版本为准。

安装后检查：

```bash
nerdctl version
nerdctl info
containerd --version
buildctl --version
```

若 containerd socket 不是默认路径，可显式指定：

```bash
nerdctl --address /run/containerd/containerd.sock info
```

namespace 同样重要：

```bash
nerdctl --namespace default ps
nerdctl --namespace k8s.io ps
```

不要轻易在 `k8s.io` namespace 中用 nerdctl 删除 Kubernetes 管理的容器和快照。

---

### 3.3.3 nerdctl 的命令行使用

常见 Docker 风格命令：

```bash
nerdctl pull nginx:alpine
nerdctl images
nerdctl run -d --name web -p 8080:80 nginx:alpine
nerdctl ps
nerdctl logs web
nerdctl exec -it web sh
nerdctl stop web
nerdctl rm web
nerdctl rmi nginx:alpine
```

从 containerd 对象模型看：

```text
nerdctl run
  ├── 解析短镜像名
  ├── Pull/Unpack image
  ├── 创建/选择网络
  ├── 创建 Container metadata
  ├── 创建 writable snapshot
  ├── 生成 OCI Spec
  ├── 创建 Task
  ├── CNI/端口映射
  └── Start Task
```

nerdctl 的便利不意味着 containerd 中出现了一个新的“Docker Container”对象。底层仍然是：

```text
Image → Snapshot → Container → Task → Process
```

#### 3.3.3.1 `ctr` 与 `nerdctl` 资源为什么可能互相看见

只要满足：

- 连接同一个 containerd socket；
- 使用同一个 containerd namespace；
- 访问同一个 metadata 和 content store；

两者可以看到相同底层资源。

但 nerdctl 可能添加自己的 labels、网络状态和命名习惯。不要用 `ctr` 随意删除 nerdctl 管理的 snapshot 或 lease，否则会破坏高层状态一致性。

---

### 3.3.4 运行容器

```bash
nerdctl run -d \
  --name web \
  --restart always \
  -p 8080:80 \
  -v /srv/html:/usr/share/nginx/html:ro \
  nginx:alpine
```

排障分层：

```bash
nerdctl ps -a
nerdctl inspect web
nerdctl logs web

ctr containers ls
ctr tasks ls
ctr snapshots ls
ctr events
```

若容器能启动但网络不通，优先区分：

```text
Task 问题
  ├── task 不存在
  ├── runtime create/start 失败
  └── OCI Spec / mount / cgroup 失败

CNI 问题
  ├── /opt/cni/bin 缺插件
  ├── /etc/cni/net.d 缺配置
  ├── IPAM 分配失败
  ├── bridge/veth 配置失败
  └── portmap/iptables/nftables 失败
```

`ctr run` 默认并不会自动提供与 Docker 一样的容器网络体验。nerdctl 正是在客户端侧补上这层编排。

---

### 3.3.5 构建镜像

```bash
nerdctl build -t example/app:v1 .
```

核心边界：

```text
nerdctl
  → 调用 BuildKit
  → BuildKit 执行 Dockerfile/LLB 构建
  → 生成 OCI/Docker image manifest、config、layers
  → 写入 containerd content store
  → 创建 image metadata
```

不是 containerd daemon 自己解析 Dockerfile。containerd 提供的是：

- content store；
- image service；
- snapshotter；
- leases；
- diff/apply；
- transfer；
- runtime。

Dockerfile 前端、构建缓存图和并行构建主要属于 BuildKit。

常用命令：

```bash
nerdctl build -t example/app:v1 .
nerdctl build --no-cache -t example/app:v2 .
nerdctl build --build-arg VERSION=1.0 -t example/app:v1 .
nerdctl build --platform linux/amd64 -t example/app:v1 .
```

构建后可从底层观察：

```bash
nerdctl images
ctr images ls
ctr content ls
ctr snapshots ls
```

---

## 3.4 三种客户端如何选择

| 场景 | 推荐工具 | 原因 |
|---|---|---|
| 查看插件初始化失败 | `ctr plugins ls` | 直接观察 containerd 插件 |
| 调试 content/snapshot | `ctr` | 底层对象最完整 |
| 手工运行普通容器 | `nerdctl` | 网络、端口、volume 体验更完整 |
| 构建镜像 | `nerdctl + BuildKit` | containerd 本身不解析 Dockerfile |
| 调试 Kubernetes Pod | `crictl` | 按 CRI PodSandbox/Container 语义 |
| 查看 Kubernetes 底层对象 | `ctr -n k8s.io` | 观察 CRI 写入的 containerd 对象 |
| 业务程序集成 | Go client / gRPC | 不依赖不稳定 CLI 输出 |

---

## 3.5 建议实验

### 实验一：观察 Container 与 Task 分离

```bash
ctr images pull docker.io/library/busybox:latest

ctr containers create docker.io/library/busybox:latest demo
ctr containers ls
ctr tasks ls

ctr tasks start -d demo
ctr tasks ls

ctr tasks kill demo
ctr tasks delete demo
ctr containers ls

ctr containers delete demo
```

观察点：

1. 创建 Container 后 Task 列表仍为空。
2. 删除 Task 后 Container 仍可存在。
3. Container 删除与 Snapshot 清理不是同一个操作。

### 实验二：观察 namespace 隔离

```bash
ctr namespaces create ns-a
ctr namespaces create ns-b

ctr -n ns-a images pull docker.io/library/busybox:latest

ctr -n ns-a images ls
ctr -n ns-b images ls
ctr -n ns-a content ls
ctr -n ns-b content ls
```

思考：

- 为什么 image list 不同？
- 底层 blob 是否可能复用？
- metadata shared policy 与 namespace 的关系是什么？

### 实验三：观察 daemon 重启与 shim 解耦

```bash
ctr run -d docker.io/library/busybox:latest survive sleep 3600

ps -ef | grep containerd
systemctl restart containerd

ctr tasks ls
ps -ef | grep containerd-shim
```

观察：

- containerd PID 改变；
- shim/容器进程是否持续；
- 重启后 Task 是否重新可见；
- `KillMode=process` 的意义。

请在测试节点操作，不要在生产节点随意重启。

---

## 3.6 containerd 1.7.1 到 2.2.5 的阅读修正

| 原书可能出现的内容 | 2.2.5 应如何理解 |
|---|---|
| 配置 `version = 2` | 当前最高配置版本为 3 |
| 单一 `[plugins."io.containerd.grpc.v1.cri"]` 配置块 | CRI image/runtime 配置已拆分，详见第 4 章 |
| Runtime v1 命令或插件 | Runtime v1 已移除，应以 Runtime v2 为主 |
| `containerd-shim-runc-v1` | 2.x 主线使用 `containerd-shim-runc-v2` |
| 将 `ctr` 当稳定运维 CLI | 源码明确声明它是 unsupported debug client |
| 认为安装 containerd 自动具备 CNI | runc 和 CNI plugins 通常需单独安装 |
| 认为 build 是 containerd 原生能力 | Dockerfile 构建主要由 BuildKit 完成 |

---

## 3.7 源码阅读路线

```text
cmd/containerd/command/main.go
  → daemon CLI 参数

cmd/containerd/command/config.go
  → config default/dump/migrate

cmd/containerd/server/config/config.go
  → 主配置结构与加载合并

containerd.service
  → systemd 生命周期策略

cmd/ctr/app/main.go
  → ctr 命令注册与定位声明

cmd/ctr/commands/namespaces/
  → namespace CLI 到 NamespaceService

cmd/ctr/commands/images/
  → pull/push/import/export

cmd/ctr/commands/run/
  → run 组合流程

cmd/ctr/commands/containers/
  → Container metadata

cmd/ctr/commands/tasks/
  → Task runtime operations

client/
  → ctr 背后的 Go client API
```

---

### 本章结论

使用 containerd 时最容易犯的错误，不是命令写错，而是对象层次混乱。

请牢牢记住：

```text
安装层：
containerd + runc + 按需安装 CNI/BuildKit

配置层：
config.toml version 3 + plugins + imports

客户端层：
ctr = 调试
nerdctl = 通用容器体验
crictl = CRI 调试

对象层：
Image ≠ Content ≠ Snapshot
Container ≠ Task ≠ Process

隔离层：
containerd namespace ≠ Linux namespace
```

理解这些边界后，第 4 章的 CRI、第 5 章的 CNI、第 6 章的存储都会自然串联起来。
