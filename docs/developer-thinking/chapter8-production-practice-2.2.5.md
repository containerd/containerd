# 《containerd 原理剖析与实战》第 8 章伴读

> **适配版本：containerd 2.2.5**
> **对应原书章节：第 8 章 containerd 生产与实践**
> **源码基线：用户提供的 `containerd-2.2.5` 源码包**

---

## 阅读说明

这一章把前面的架构转化为三个实践能力：

1. 让 containerd 暴露可观测指标，并理解指标来自哪一层；
2. 用 containerd 2.x Go client 正确管理 Image、Container、Task 和清理；
3. 基于 vendored NRI v0.11.0 开发节点侧运行时插件。

需要先明确边界：

- Prometheus 和 Grafana 是外部项目，containerd 源码只负责指标暴露；
- Go client 的 module path 在 2.x 已变为 `github.com/containerd/containerd/v2/client`；
- NRI 插件在容器启动关键路径上，正确性和超时控制比“功能能跑”更重要。

---

## 8.1 containerd 监控实践

### 8.1.1 安装 Prometheus

#### 8.1.1.1 containerd 不内置 Prometheus Server

containerd 内置的是 Prometheus exposition endpoint：

```text
containerd metrics listener
  └── GET /v1/metrics
```

Prometheus 则是独立进程：

```text
Prometheus
  │ 定时 HTTP scrape
  ▼
containerd /v1/metrics
  │
  ├── 存储时序数据
  ├── PromQL 查询
  └── 告警规则
```

Grafana 再从 Prometheus 查询并展示。

#### 8.1.1.2 containerd 2.2.5 的 metrics 配置

源码结构：

```go
type MetricsConfig struct {
    Address       string `toml:"address"`
    GRPCHistogram bool   `toml:"grpc_histogram"`
}
```

位置：

```text
cmd/containerd/server/config/config.go
```

配置示例：

```toml
[metrics]
  address = "127.0.0.1:1338"
  grpc_histogram = false
```

重启：

```bash
systemctl restart containerd
systemctl status containerd
ss -lntp | grep 1338
```

验证：

```bash
curl -fsS http://127.0.0.1:1338/v1/metrics | head
```

源码 `Server.ServeMetrics()` 明确只注册：

```go
m.Handle("/v1/metrics", metrics.Handler())
```

所以路径不是默认的 `/metrics`，而是：

```text
/v1/metrics
```

#### 8.1.1.3 监听地址的安全选择

推荐顺序：

```text
127.0.0.1:1338
  + 本机 Prometheus/agent 抓取

节点内网 IP:1338
  + 防火墙只允许监控网段

0.0.0.0:1338
  + 必须额外做网络访问控制
```

metrics 可能暴露：

- namespace；
- container/task ID；
- image/runtime 状态；
- 资源使用；
- RPC 方法与错误；
- daemon 版本。

不要无控制暴露到公网。

#### 8.1.1.4 systemd 配置检查

containerd metrics listener 由同一 daemon 创建，不需要单独 systemd service。配置后若无端口：

```bash
containerd config dump | sed -n '/^\[metrics\]/,/^\[/p'
journalctl -u containerd -b | grep -i metrics
```

确认：

- 启动读取的是哪份 `--config`；
- address 未被 imports 覆盖；
- 端口未占用；
- listener 地址合法；
- daemon 确实是 2.2.5 配置格式。

---

### 8.1.2 Prometheus 上 containerd 的指标采集配置

#### 8.1.2.1 最小 scrape 配置

```yaml
scrape_configs:
  - job_name: containerd
    metrics_path: /v1/metrics
    static_configs:
      - targets:
          - 10.0.0.11:1338
          - 10.0.0.12:1338
          - 10.0.0.13:1338
        labels:
          cluster: prod-a
```

Prometheus reload 后验证 Targets 页面或查询：

```promql
up{job="containerd"}
```

`up=1` 只说明 HTTP scrape 成功，不说明所有 containerd 插件健康。

#### 8.1.2.2 Kubernetes 节点发现思路

若 Prometheus 运行在 Kubernetes 中，可以通过 node service、DaemonSet sidecar/agent、静态节点列表或 service discovery 获取各节点 metrics endpoint。

设计时要处理：

- 节点地址变化；
- hostNetwork；
- 防火墙；
- TLS/认证代理；
- 节点标签 relabel；
- container ID 高基数。

containerd 自身 metrics endpoint 没有提供完整鉴权层，常见做法是在节点本地抓取，或通过受控 exporter/proxy 暴露。

#### 8.1.2.3 指标来源

containerd 2.2.5 指标主要来自多处注册：

```text
core/metrics
  └── containerd_build_info

plugins/gc
  └── GC collections / duration

core/metrics/cgroups
  ├── cgroup v1 collector
  └── cgroup v2 collector

server gRPC middleware
  └── RPC request/result/latency

Go runtime / process collector
  └── go_* / process_*
```

cgroup task monitor 插件：

```text
io.containerd.monitor.task.v1.cgroups
```

在 Linux 上根据：

```go
cgroups.Mode() == cgroups.Unified
```

选择 cgroup v2 或 v1 collector。

#### 8.1.2.4 `no_prometheus`

cgroup monitor 配置：

```toml
[plugins."io.containerd.monitor.task.v1.cgroups"]
  no_prometheus = false
```

设置为 true 会保留 task monitor 其他职责，但不注册 per-container Prometheus namespace。高密度节点若遇到指标采集开销或高基数问题，可以评估；但关闭后会失去重要容器 CPU、内存、I/O 指标。

#### 8.1.2.5 gRPC histogram

```toml
[metrics]
  grpc_histogram = true
```

server 会启用：

```go
grpc_prometheus.WithServerHandlingTimeHistogram()
```

好处：可分析 RPC 延迟分布；代价：每个 service/method/status 组合增加 histogram bucket 时序，显著提高基数和采集量。

生产建议：

- 先关闭；
- 遇到 API 延迟问题时在可控范围开启；
- 评估 TSDB 容量；
- 用 recording rules 聚合；
- 避免长期开启无用 bucket。

#### 8.1.2.6 指标命名不要靠背诵

不同构建、平台、cgroup mode 和插件状态会让指标集合变化。先查询当前节点：

```bash
curl -fsS http://127.0.0.1:1338/v1/metrics \
  | grep -E '^# (HELP|TYPE)|^[a-zA-Z_:][a-zA-Z0-9_:]*' \
  | less
```

只看 metric name：

```bash
curl -fsS http://127.0.0.1:1338/v1/metrics \
  | awk '!/^#/ {print $1}' \
  | sed 's/{.*//' \
  | sort -u
```

再根据 HELP/TYPE 建 dashboard，避免照搬旧版本指标名。

#### 8.1.2.7 Counter、Gauge、Histogram 的读法

| 类型 | 含义 | 查询方式 |
|---|---|---|
| Counter | 只累计，重启归零 | `rate()` / `increase()` |
| Gauge | 可增可减的当前值 | 直接取值、`max_over_time()` |
| Histogram | bucket + sum + count | `histogram_quantile()` |

错误示例：直接对 CPU 累计秒数求和。正确方向：

```promql
rate(<cpu_usage_seconds_total>[5m])
```

具体 metric name 应从 2.2.5 实际 endpoint 获取。

#### 8.1.2.8 核心监控维度

建议覆盖：

```text
Daemon
├── up
├── process CPU/RSS/fd
├── Go goroutine/GC
└── build version

API
├── request rate
├── error rate
└── latency

GC
├── collections
└── duration

Task/cgroup
├── CPU
├── memory current/limit/events
├── I/O bytes/ops
├── pids
└── OOM

Node storage（需 node exporter/外部监控）
├── /var/lib/containerd capacity/inodes
├── snapshotter backend
└── devmapper thin pool
```

containerd metrics 不能替代 node exporter、kubelet/cAdvisor、CNI 和存储后端监控。

#### 8.1.2.9 告警思路

不要只设置“containerd 进程 down”。至少考虑：

1. metrics scrape down；
2. containerd gRPC 错误率突升；
3. RPC p99 延迟；
4. GC 持续变慢；
5. task OOM / memory events；
6. snapshotter/root 文件系统空间和 inode；
7. shim 数异常增长；
8. kubelet CRI Status not ready；
9. 镜像拉取和 task create 失败率。

API 与业务层告警需要结合日志或 Kubernetes metrics，containerd 内置 endpoint 并不暴露所有 kubelet 语义。

---

### 8.1.3 Grafana 监控配置

#### 8.1.3.1 数据源

Grafana 只需配置 Prometheus 数据源。containerd endpoint 通常不应让 Grafana 直接逐节点查询，因为 Grafana 不是时序采集器。

```text
containerd → Prometheus → Grafana
```

#### 8.1.3.2 Dashboard 变量

建议变量：

```text
cluster
instance
namespace
container_id
runtime
snapshotter
```

注意 container ID 高基数。默认面板优先聚合到：

- cluster；
- node；
- namespace；
- workload。

containerd 原生指标未必直接带 Kubernetes workload 名，需要 Prometheus relabel、kube-state-metrics 或其他映射补充。

#### 8.1.3.3 面板分层

一张可用的 Dashboard：

```text
第一行：节点/daemon 总览
  up、版本、进程 CPU/RSS、goroutines

第二行：API 健康
  QPS、错误率、p50/p95/p99

第三行：运行容器资源
  CPU、内存、OOM、PIDs

第四行：I/O 与存储
  block IO、GC duration、磁盘空间（外部指标）

第五行：异常明细
  error methods、top containers、restart correlations
```

#### 8.1.3.4 Histogram 查询示意

```promql
histogram_quantile(
  0.99,
  sum by (le, grpc_service, grpc_method) (
    rate(grpc_server_handling_seconds_bucket{job="containerd"}[5m])
  )
)
```

实际标签和 metric name以 endpoint 输出为准。不要直接复制后发现全是 No data。

#### 8.1.3.5 与日志联动

Dashboard 上出现 task create error spike 时，应能跳转日志查询：

```text
node=<instance>
containerd unit
时间窗口 ±5m
关键词 shim/runc/snapshot/CNI/CRI
```

指标回答“何时、多少、趋势”；日志回答“哪个对象、什么错误、调用链”。

---

### 8.1.4 配置 containerd 面板

#### 8.1.4.1 先构建 recording rules

示意：

```yaml
groups:
  - name: containerd.rules
    rules:
      - record: job:containerd_grpc_requests:rate5m
        expr: sum by (job, instance, grpc_service, grpc_method) (
                rate(grpc_server_handled_total{job="containerd"}[5m])
              )

      - record: job:containerd_grpc_errors:rate5m
        expr: sum by (job, instance, grpc_service, grpc_method) (
                rate(grpc_server_handled_total{
                  job="containerd",
                  grpc_code!="OK"
                }[5m])
              )
```

指标名和标签需按实际输出调整。Recording rule 的价值：

- Dashboard 查询更快；
- 统一聚合口径；
- 降低复杂 PromQL 重复；
- 告警和面板复用。

#### 8.1.4.2 版本面板

`containerd_build_info` 可展示 version/revision。升级期间按节点统计不同版本，有助于发现漏升级节点。

```promql
count by (version, revision) (containerd_build_info)
```

#### 8.1.4.3 GC 面板

关注：

- collection 次数按 status；
- GC duration；
- 与镜像拉取/删除、磁盘 I/O 的相关性。

GC 偶尔变长未必异常；持续增长且伴随 API 卡顿、磁盘高延迟才更值得告警。

#### 8.1.4.4 容器资源面板

cgroup v1/v2 指标字段不同。建议在 datasource 中检查实际 metric family，再做兼容 recording rules，把两套统一为自定义名称，例如：

```text
node_namespace_container:cpu_usage_seconds:rate5m
node_namespace_container:memory_current_bytes
node_namespace_container:io_bytes:rate5m
```

#### 8.1.4.5 Dashboard 验收

至少完成：

1. 停止一个实验节点 containerd，`up` 告警触发；
2. 创建/删除大量测试容器，API 和 GC 面板有变化；
3. 制造短任务，task metrics 能出现并正确消失；
4. 升级一个节点，version 面板能区分；
5. Prometheus 重启后数据和告警恢复；
6. Dashboard 不因 container ID 高基数卡死。

---

## 8.2 基于 containerd 开发自己的容器客户端

### 8.2.1 初始化 Client

#### 8.2.1.1 2.x import path

```go
import containerd "github.com/containerd/containerd/v2/client"
```

不是旧的：

```go
github.com/containerd/containerd
```

相关包：

```go
github.com/containerd/containerd/v2/pkg/cio
github.com/containerd/containerd/v2/pkg/namespaces
github.com/containerd/containerd/v2/pkg/oci
```

#### 8.2.1.2 创建 client

```go
client, err := containerd.New("/run/containerd/containerd.sock")
if err != nil {
    return err
}
defer client.Close()
```

Client 是 gRPC client 与多个 service wrapper 的聚合，不等于一个容器。

#### 8.2.1.3 Namespace Context

```go
ctx := namespaces.WithNamespace(context.Background(), "example")
```

containerd Native API 大部分操作必须带 namespace。一个常见 bug：

```text
Pull 用 example namespace
Create 用 default namespace
  ↓
Create 找不到 image
```

应从入口构造 ctx 并贯穿全部调用。

#### 8.2.1.4 Timeout

```go
ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
defer cancel()
```

不要对容器整个运行寿命只用一个很短 context。可以分别为：

- pull；
- create；
- start；
- stop；
- cleanup；

创建独立 timeout。Cleanup 建议使用新的 background context，避免原请求取消后清理也立刻失败。

---

### 8.2.2 拉取镜像

```go
image, err := client.Pull(
    ctx,
    "docker.io/library/busybox:latest",
    containerd.WithPullUnpack,
)
```

`WithPullUnpack` 很重要，它会把 rootfs layers 解包到 snapshotter。否则后续创建 snapshot 时可能触发额外 unpack 或报未解包。

#### 8.2.2.1 指定平台和 snapshotter

```go
image, err := client.Pull(
    ctx,
    ref,
    containerd.WithPullUnpack,
    containerd.WithPullSnapshotter("overlayfs"),
)
```

跨架构场景还应设置 platform matcher。不要在 amd64 节点默认拉取 arm64 manifest 后再把 `exec format error` 误判为 runtime 故障。

#### 8.2.2.2 Resolver 与认证

生产客户端通常还要处理：

- private registry credentials；
- hosts.toml；
- mirror；
- TLS CA；
- retry/backoff；
- progress；
- content lease。

不要把用户名密码直接写进镜像 URL 或日志。

#### 8.2.2.3 Image 是 metadata wrapper

`image` 对象提供：

```text
Name
Target
Config
RootFS
Unpack
IsUnpacked
```

它引用 content store 中的 descriptor 图。不要假设 Pull 返回后所有 blob 都在某个 image 专属目录。

---

### 8.2.3 创建 OCI Spec

#### 8.2.3.1 以镜像 config 为基础

```go
containerd.WithNewSpec(
    oci.WithImageConfig(image),
)
```

`WithImageConfig` 会应用：

- Entrypoint/Cmd；
- Env；
- WorkingDir；
- User；
- 其他镜像配置。

再叠加：

```go
oci.WithProcessArgs("sh", "-c", "echo hello")
```

后面的 SpecOpts 会修改前面生成的 spec。

#### 8.2.3.2 SpecOpts 是函数式修改器

概念：

```go
type SpecOpts func(ctx, client, container, *oci.Spec) error
```

可组合：

```go
containerd.WithNewSpec(
    oci.WithImageConfig(image),
    oci.WithProcessArgs("sleep", "300"),
    oci.WithEnv([]string{"APP_ENV=demo"}),
    oci.WithHostname("demo"),
)
```

不同 Opt 顺序可能影响最终值。生产代码最好把安全基线封装成统一函数。

#### 8.2.3.3 安全基线

不要默认：

```text
privileged
host PID/network
bind mount /
全部 capabilities
seccomp unconfined
```

应明确：

- user；
- capabilities；
- readonly rootfs；
- seccomp；
- AppArmor/SELinux；
- cgroup resources；
- mounts；
- namespace paths。

Native API 不会替你应用 Kubernetes PodSecurity 策略。

---

### 8.2.4 创建 Task

#### 8.2.4.1 先创建 Container metadata

```go
container, err := client.NewContainer(
    ctx,
    id,
    containerd.WithImage(image),
    containerd.WithNewSnapshot(id+"-snapshot", image),
    containerd.WithNewSpec(
        oci.WithImageConfig(image),
        oci.WithProcessArgs("sh", "-c", "echo hello from containerd"),
    ),
)
```

此时：

```text
Container metadata 已创建
Snapshot active 已创建
Task 尚不存在
Shim 尚未启动
容器进程尚不存在
```

#### 8.2.4.2 创建 IO

直接继承当前终端：

```go
task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
```

无 IO：

```go
task, err := container.NewTask(ctx, cio.NullIO)
```

自定义输出：

```go
var stdout bytes.Buffer
creator := cio.NewCreator(cio.WithStreams(nil, &stdout, os.Stderr))
task, err := container.NewTask(ctx, creator)
```

IO creator 在 Task 创建阶段建立 FIFO/stream，之后才由 shim 接管。

#### 8.2.4.3 NewTask 做了什么

调用链：

```text
Container.NewTask
  ↓
Tasks/Create gRPC
  ↓
读取 Container Spec/Runtime/Snapshot
  ↓
Runtime v2 TaskManager.Create
  ↓
创建 bundle
  ↓
启动/复用 shim
  ↓
默认 `io.containerd.runc.v2` shim 调 runc create
```

这里的 `runc` 是默认 runtime handler 的路径；其他 Runtime v2 shim 可采用不同的运行时引擎和实现。应用命令仍未执行，Task 处于 created。

---

### 8.2.5 启动 Task

#### 8.2.5.1 先 Wait 再 Start

```go
exitC, err := task.Wait(ctx)
if err != nil {
    return err
}

if err := task.Start(ctx); err != nil {
    return err
}

status := <-exitC
code, exitedAt, err := status.Result()
```

先注册 Wait 可避免极短进程退出竞态。

#### 8.2.5.2 `Task.Start()` 的真实意义

```text
默认 runc v2 shim 的 Task.Start
  ↓
runc start
  ↓
容器 init exec 用户命令
```

`NewTask()` 只达到 OCI created 状态，`Start()` 才真正运行。

#### 8.2.5.3 状态

```go
st, err := task.Status(ctx)
```

典型：

```text
Created
Running
Stopped
Paused
Pausing
Unknown
```

状态查询是瞬时快照。并发操作中要用 event/Wait 和幂等错误处理，而不是只依据一次 Status。

---

### 8.2.6 停止 Task

#### 8.2.6.1 优雅停止

```go
exitC, err := task.Wait(ctx)
if err != nil {
    return err
}

if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
    return err
}

select {
case status := <-exitC:
    code, _, err := status.Result()
    _ = code
    _ = err
case <-time.After(10 * time.Second):
    if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
        return err
    }
    <-exitC
}
```

完整实现还要处理：

- Task 已停止；
- not found；
- context cancelled；
- Kill all processes；
- SIGTERM 被应用忽略；
- Wait channel 已消费。

#### 8.2.6.2 删除顺序

```text
Task stopped
  ↓
Task.Delete
  ↓
Container.Delete
  ↓
WithSnapshotCleanup
```

代码：

```go
_, err = task.Delete(ctx)
err = container.Delete(ctx, containerd.WithSnapshotCleanup)
```

Task.Delete 返回 exit status 和 error，不是只有 error。

#### 8.2.6.3 Cleanup Context

若主 ctx 已超时：

```go
cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
cleanupCtx = namespaces.WithNamespace(cleanupCtx, namespace)
```

再执行 Delete。namespace 必须重新附加。

#### 8.2.6.4 幂等清理

生产代码要把：

```text
not found
already stopped
already exists
failed precondition
```

按语义处理。清理阶段对 not found 往往可视为成功，但权限、I/O、snapshot busy 不能忽略。

---

### 8.2.7 运行示例

下面给出一个完整的一次性客户端。它：

1. 连接 daemon；
2. 使用独立 namespace；
3. 拉取并解包镜像；
4. 创建 Container + Snapshot + OCI Spec；
5. 创建 Task；
6. Wait 后 Start；
7. 输出退出码；
8. 清理 Task、Container 和 snapshot。

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    containerd "github.com/containerd/containerd/v2/client"
    "github.com/containerd/containerd/v2/pkg/cio"
    "github.com/containerd/containerd/v2/pkg/namespaces"
    "github.com/containerd/containerd/v2/pkg/oci"
    "github.com/containerd/errdefs"
)

const (
    socketPath = "/run/containerd/containerd.sock"
    namespace  = "sdk-demo"
    imageRef   = "docker.io/library/busybox:latest"
    containerID = "hello-containerd"
)

func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
    }
}

func run() error {
    client, err := containerd.New(socketPath)
    if err != nil {
        return fmt.Errorf("connect containerd: %w", err)
    }
    defer client.Close()

    baseCtx := namespaces.WithNamespace(context.Background(), namespace)

    pullCtx, cancelPull := context.WithTimeout(baseCtx, 5*time.Minute)
    image, err := client.Pull(
        pullCtx,
        imageRef,
        containerd.WithPullUnpack,
        containerd.WithPullSnapshotter("overlayfs"),
    )
    cancelPull()
    if err != nil {
        return fmt.Errorf("pull image: %w", err)
    }

    // 为了让示例可重复运行，先清理同名残留。
    if old, err := client.LoadContainer(baseCtx, containerID); err == nil {
        cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        cleanupErr := cleanupContainer(
            namespaces.WithNamespace(cleanupCtx, namespace), old,
        )
        cancel()
        if cleanupErr != nil {
            return fmt.Errorf("clean up existing container: %w", cleanupErr)
        }
    } else if !errdefs.IsNotFound(err) {
        return fmt.Errorf("check existing container: %w", err)
    }

    container, err := client.NewContainer(
        baseCtx,
        containerID,
        containerd.WithImage(image),
        containerd.WithSnapshotter("overlayfs"),
        containerd.WithNewSnapshot(containerID+"-snapshot", image),
        containerd.WithNewSpec(
            oci.WithImageConfig(image),
            oci.WithProcessArgs(
                "sh", "-c",
                "echo hello from containerd 2.2.5; uname -a",
            ),
        ),
    )
    if err != nil {
        return fmt.Errorf("create container metadata: %w", err)
    }

    // 无论后续在哪一步失败，都尝试删除 container 和 snapshot。
    defer func() {
        cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        cleanupCtx = namespaces.WithNamespace(cleanupCtx, namespace)
        _ = container.Delete(cleanupCtx, containerd.WithSnapshotCleanup)
    }()

    task, err := container.NewTask(baseCtx, cio.NewCreator(cio.WithStdio))
    if err != nil {
        return fmt.Errorf("create task: %w", err)
    }

    taskDeleted := false
    defer func() {
        if taskDeleted {
            return
        }
        cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        cleanupCtx = namespaces.WithNamespace(cleanupCtx, namespace)
        _, _ = task.Delete(cleanupCtx, containerd.WithProcessKill)
    }()

    exitC, err := task.Wait(baseCtx)
    if err != nil {
        return fmt.Errorf("wait task: %w", err)
    }

    if err := task.Start(baseCtx); err != nil {
        return fmt.Errorf("start task: %w", err)
    }

    status := <-exitC
    code, exitedAt, resultErr := status.Result()
    if resultErr != nil {
        return fmt.Errorf("read exit status: %w", resultErr)
    }

    fmt.Printf("container exited: code=%d at=%s\n", code, exitedAt.Format(time.RFC3339Nano))

    if _, err := task.Delete(baseCtx); err != nil && !errdefs.IsNotFound(err) {
        return fmt.Errorf("delete task: %w", err)
    }
    taskDeleted = true

    if err := container.Delete(baseCtx, containerd.WithSnapshotCleanup); err != nil && !errdefs.IsNotFound(err) {
        return fmt.Errorf("delete container: %w", err)
    }

    // 避免 defer 再删除时报错；Delete 应按幂等方式处理。
    return nil
}

func cleanupContainer(ctx context.Context, c containerd.Container) error {
    task, err := c.Task(ctx, nil)
    if err == nil {
        if _, deleteErr := task.Delete(ctx, containerd.WithProcessKill); deleteErr != nil && !errdefs.IsNotFound(deleteErr) {
            return fmt.Errorf("delete old task: %w", deleteErr)
        }
    } else if !errdefs.IsNotFound(err) {
        return fmt.Errorf("load old task: %w", err)
    }

    if err := c.Delete(ctx, containerd.WithSnapshotCleanup); err != nil && !errdefs.IsNotFound(err) {
        return fmt.Errorf("delete old container: %w", err)
    }
    return nil
}

```

`go.mod`：

```go
module example.com/containerd-client-demo

go 1.25

require (
    github.com/containerd/containerd/v2 v2.2.5
    github.com/containerd/errdefs v1.0.0
)
```

运行：

```bash
go mod tidy
sudo go run .
```

源码包 `go.mod` 声明 Go 1.25.0。使用较老 Go 工具链时，可能无法编译该版本依赖，应使用与模块要求匹配的工具链。

#### 8.2.7.1 示例中的一个细节

上例主路径显式删除 container，defer 中还保留兜底删除。生产代码应把清理封装成“忽略 not found”的幂等函数，避免双删日志噪声。示例保留这种结构，是为了展示任一步失败后仍能清理。

#### 8.2.7.2 不应照搬的部分

- 固定 `latest`；
- 使用 root 运行；
- 无 registry 认证；
- 无日志轮转；
- 无资源限制；
- 无 seccomp/LSM 强化；
- 只使用一个固定 ID；
- 未实现完整 retry/backoff。

它是 API 生命周期示例，不是完整生产编排器。

---

## 8.3 开发自己的 NRI 插件

### 8.3.1 插件定义与接口实现

#### 8.3.1.1 目标

实现一个简单插件：

- 观察 `CreateContainer`；
- 为匹配 annotation 的容器增加环境变量；
- 记录 Start/Stop；
- 不进行高风险 mount/device 修改。

使用 containerd 2.2.5 vendored：

```text
github.com/containerd/nri v0.11.0
```

#### 8.3.1.2 接口

```go
CreateContainer(
    context.Context,
    *api.PodSandbox,
    *api.Container,
) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
```

`ContainerAdjustment` 提供辅助方法：

```text
AddAnnotation
AddMount
AddEnv
AddHooks
AddRlimit
AddDevice
AddCDIDevice
AddOrReplaceNamespace
AddLinuxUnified
...
```

源码：

```text
vendor/github.com/containerd/nri/pkg/api/adjustment.go
```

#### 8.3.1.3 完整插件示例

```go
package main

import (
    "context"
    "fmt"
    "log"
    "strings"

    "github.com/containerd/nri/pkg/api"
    "github.com/containerd/nri/pkg/stub"
)

const enableAnnotation = "example.com/nri-demo"

type plugin struct{}

func (p *plugin) CreateContainer(
    ctx context.Context,
    pod *api.PodSandbox,
    ctr *api.Container,
) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
    podID := pod.GetId()
    containerID := ctr.GetId()

    enabled := false
    for k, v := range ctr.GetAnnotations() {
        if k == enableAnnotation && strings.EqualFold(v, "enabled") {
            enabled = true
            break
        }
    }

    log.Printf("CreateContainer pod=%s container=%s enabled=%v", podID, containerID, enabled)

    if !enabled {
        return nil, nil, nil
    }

    adjust := &api.ContainerAdjustment{}
    adjust.AddEnv("NRI_DEMO", "enabled")
    adjust.AddAnnotation("example.com/nri-demo-applied", "true")

    return adjust, nil, nil
}

func (p *plugin) StartContainer(
    ctx context.Context,
    pod *api.PodSandbox,
    ctr *api.Container,
) error {
    log.Printf("StartContainer pod=%s container=%s", pod.GetId(), ctr.GetId())
    return nil
}

func (p *plugin) StopContainer(
    ctx context.Context,
    pod *api.PodSandbox,
    ctr *api.Container,
) ([]*api.ContainerUpdate, error) {
    log.Printf("StopContainer pod=%s container=%s", pod.GetId(), ctr.GetId())
    return nil, nil
}

func main() {
    s, err := stub.New(
        &plugin{},
        stub.WithPluginName("env-injector"),
        stub.WithPluginIdx("10"),
    )
    if err != nil {
        log.Fatal(fmt.Errorf("create NRI stub: %w", err))
    }

    if err := s.Run(context.Background()); err != nil {
        log.Fatal(fmt.Errorf("run NRI plugin: %w", err))
    }
}
```

这段代码实现了三个 vendored stub 接口，因此 stub 会自动计算需要订阅的事件。无需手写 EventMask；只有需要自定义 Configure 行为时才实现 `ConfigureInterface`。

#### 8.3.1.4 选择 annotation 来源

NRI `api.Container` 中的 annotations 来自 CRI/OCI 转换和 runtime 配置允许传递的 annotation。并不是 Kubernetes Pod 的所有 annotation 都必然到达 NRI。

CRI runtime handler 可配置：

```toml
pod_annotations = ["example.com/*"]
container_annotations = ["example.com/*"]
```

是否需要配置取决于 CRI/NRI 当前传递逻辑和 annotation 类型。测试时必须打印允许的 key，而不是假设一定存在。

#### 8.3.1.5 调整冲突

多个 NRI plugin 可能同时改环境变量或 annotation。插件顺序由 index 决定，validator 负责检查非法/冲突 adjustment。

设计建议：

- 使用厂商域名前缀；
- 不覆盖用户已有变量，除非策略明确；
- 记录调整所有者；
- 对同一请求保持确定性；
- 不依赖其他插件恰好先运行，除非定义顺序合同。

---

### 8.3.2 插件实例化与启动

#### 8.3.2.1 go.mod

```go
module example.com/nri-env-injector

go 1.25

require github.com/containerd/nri v0.11.0
```

构建：

```bash
go mod tidy
go build -trimpath -o 10-env-injector .
```

#### 8.3.2.2 containerd NRI 配置

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

实际默认配置：

```bash
containerd config default | sed -n '/io.containerd.nri.v1.nri/,/^\[/p'
```

#### 8.3.2.3 安装权限

```bash
sudo install -d -m 0755 /opt/nri/plugins
sudo install -m 0755 10-env-injector /opt/nri/plugins/10-env-injector
sudo chown -R root:root /opt/nri/plugins
```

更严格环境可让目录 0750，并限制 containerd 服务用户可读执行。关键是普通用户不能写入。

#### 8.3.2.4 由 containerd 启动还是外部启动

**runtime-managed：** 把二进制放到 `plugin_path`，containerd/NRI adaptation 启动。

优点：

- 统一启动顺序；
- 身份/index 约定清晰；
- 生命周期与 runtime 集成。

**external：** 插件作为 systemd/DaemonSet 进程，主动连接 `socket_path`。

优点：

- 独立重启和发布；
- 自定义资源限制；
- 更容易加 health check。

若 `disable_connections=true`，外部连接模式不可用。

#### 8.3.2.5 配置热更新

NRI plugin-specific 配置通常在注册 Configure 阶段传入。配置文件变化是否自动重载取决于 adaptation/plugin 设计，不能假设像 CNI fsnotify 一样自动更新。

稳妥流程：

1. 修改配置；
2. 重启/重连插件；
3. Configure；
4. Synchronize；
5. 验证现有容器与新容器行为。

#### 8.3.2.6 超时与资源限制

插件在关键路径上，建议：

```text
request p99 << plugin_request_timeout
```

避免同步调用：

- 外部 HTTP；
- DNS；
- 慢数据库；
- 大规模全节点扫描。

外部状态可由后台 goroutine 预取到内存快照，请求路径只做常数时间决策。

---

### 8.3.3 插件的运行演示

#### 8.3.3.1 启动验证

```bash
systemctl restart containerd
journalctl -u containerd -f | grep -i nri
ctr plugins list | grep nri
```

期望看到：

- NRI adaptation 初始化；
- 插件被发现/连接；
- registration 成功；
- Configure；
- Synchronize。

#### 8.3.3.2 用 Kubernetes 验证

Pod 示例：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nri-demo
  annotations:
    example.com/nri-demo: enabled
spec:
  restartPolicy: Never
  containers:
    - name: main
      image: busybox:latest
      command: ["sh", "-c", "env | grep NRI_DEMO; sleep 30"]
```

创建：

```bash
kubectl apply -f nri-demo.yaml
kubectl logs nri-demo
```

期望：

```text
NRI_DEMO=enabled
```

若没有，依次检查：

1. 插件收到 CreateContainer 吗；
2. annotation 是否传入；
3. adjustment 是否被 validator 拒绝；
4. CRI runtime handler 是否允许 annotation；
5. Pod 是否使用预期节点/runtime；
6. 容器 env 中是否已被同名值覆盖。

#### 8.3.3.3 用 crictl/ctr 下钻

```bash
crictl ps -a --name nri-demo
crictl inspect <container-id>
ctr -n k8s.io containers info <container-id>
```

OCI Spec 位置可通过 container metadata 或 runtime bundle 检查。注意运行时 bundle 是内部状态，不要修改。

#### 8.3.3.4 失败演练

仅实验节点：让插件在 CreateContainer 睡眠超过 `plugin_request_timeout`，观察：

```text
CRI CreateContainer 延迟
NRI timeout 日志
Pod event
containerd 请求失败/回滚
```

然后立即恢复。这个实验用于证明插件是同步关键路径组件。

#### 8.3.3.5 升级策略

滚动升级 NRI plugin 时：

1. 新旧版本需兼容当前 NRI API；
2. 插件重连后正确 Synchronize；
3. 调整逻辑对已有容器幂等；
4. 不因 index/name 改变被视为第二个插件；
5. 先灰度节点；
6. 监控 Pod 创建延迟与失败率；
7. 保留快速 disable/rollback。

#### 8.3.3.6 生产检查清单

```text
安全
├── 二进制和配置 root-owned
├── 最小 adjustment 权限
├── 不泄露 secret
└── validator 策略

可靠性
├── timeout
├── 无阻塞外部调用
├── 幂等
├── Synchronize
└── 崩溃自动恢复

可观测性
├── event count
├── request duration
├── error/timeout
├── adjustment summary
└── plugin version

发布
├── API version compatibility
├── 灰度
├── rollback
└── 节点差异检查
```

---

## 8.4 生产运行的综合建议

### 8.4.1 containerd 升级

升级前：

```bash
containerd --version
containerd config dump
ctr plugins list
crictl info
runc --version
```

保存：

- 生效配置；
- plugin 状态；
- runtime/snapshotter 列表；
- 关键 metrics 基线；
- 运行容器数量；
- shim 数；
- root/state 磁盘与 inode。

升级后重点验证 config migration、CRI 三插件、shim reconnect 和 CNI ready。

### 8.4.2 日志

systemd journal：

```bash
journalctl -u containerd -S -30m
```

调试可临时提高：

```toml
[debug]
  level = "debug"
```

不要长期在高密度节点开启 trace/debug，日志量可能很大并包含对象细节。

### 8.4.3 资源与容量

监控：

```text
/var/lib/containerd bytes/inodes
/run/containerd bytes/inodes
open files
shim/task count
bbolt size/latency
snapshotter backend
content ingest
GC duration
```

`TasksMax=infinity` 只是不让 systemd 对 containerd cgroup施加默认 task 上限，并不代表节点 PID 无限。仍受 kernel pid_max、cgroup pids、ulimit 和资源容量约束。

### 8.4.4 Socket 权限

```bash
stat /run/containerd/containerd.sock
```

访问 containerd Socket 的主体可以执行强权限操作。不要为了让脚本方便而 `chmod 666`。使用 root、受控 group、sudo wrapper 或隔离 API proxy。

### 8.4.5 备份重点

containerd 不是业务数据备份系统。真正要备份的是：

- 配置；
- registry 中镜像；
- Kubernetes manifests/etcd；
- CSI volumes；
- NRI/CNI 配置；
- 私有 CA/hosts 配置；
- snapshotter 后端必要元数据。

容器 writable layer 应被视为可重建。

---

## 8.5 与 containerd 1.7.1 参考书对照

| 原书内容 | containerd 2.2.5 更新 |
|---|---|
| metrics endpoint 只需开端口 | 明确路径 `/v1/metrics`、gRPC histogram 基数和 cgroup v1/v2 collector |
| Go client import `github.com/containerd/containerd` | 2.x 使用 `github.com/containerd/containerd/v2/client` |
| client 示例只关注 happy path | 必须处理 namespace、Wait-before-Start、Task/Container 分层和幂等 cleanup |
| Pull 即可运行 | 区分 Pull 与 `WithPullUnpack` |
| NRI 早期示例 | 以 vendored NRI v0.11.0 stub 接口、validator、sync、timeout 为准 |
| Grafana 面板照搬模板 | 先从当前 endpoint 发现 metric names，再建立 recording rules |
| containerd 监控覆盖节点全部问题 | 还需 node exporter、kubelet/CRI、CNI、snapshotter/backend 监控 |

---

## 8.6 本章结论

1. containerd 2.2.5 通过 `[metrics]` 开启 HTTP listener，固定暴露 `/v1/metrics`。
2. 指标来自 daemon、gRPC middleware、GC 和 cgroup task monitor；启用 histogram 会增加大量时序。
3. Grafana 应通过 Prometheus 查询，Dashboard 要分 daemon、API、task、GC、storage 多层。
4. 2.x Go client 使用 `/v2/client`，所有操作必须贯穿 containerd namespace context。
5. 正确生命周期是 Pull/Unpack → NewContainer → NewTask → Wait → Start → Task.Delete → Container.Delete + SnapshotCleanup。
6. 清理应使用独立 context 并处理 not found 等幂等语义。
7. NRI 插件可观察和调整容器，但位于同步关键路径，必须重视 timeout、validator、安全、同步和灰度升级。
