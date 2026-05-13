# CRI 持久化容器 Writable Layer 方案说明

## 背景

containerd 本来就会持久化镜像 layer，但默认不会持久化容器运行时生成的 writable layer。

在 CRI 默认流程里：

- 创建容器时，会基于镜像 rootfs 创建一个新的 writable snapshot
- 删除容器时，会连同这个 writable snapshot 一起 cleanup

因此，容器内对 rootfs 的修改会在容器删除后丢失。

本方案的目标不是改 Kubernetes Volume 语义，也不是持久化镜像 layer，而是让容器的 writable layer 可以在删除 Pod 后继续复用。

目标语义是：

- 删除 Pod：视为重启，保留 writable layer
- 重新创建 Pod：继续复用原来的 writable layer
- 同一持久层只允许单节点单 writer 使用

## 基本原理

containerd 的容器 rootfs 由 snapshotter 管理。可以把它理解成“镜像层 + 容器可写层”的组合。

逻辑上大致是：

```text
image layer 1 (readonly snapshot)
image layer 2 (readonly snapshot)
image layer 3 (readonly snapshot)
container writable layer (active snapshot)
```

其中：

- 镜像 layer 通常对应 committed/readonly snapshot
- 容器 writable layer 对应 active/writable snapshot

容器删除后之所以会丢数据，不是因为镜像层没保存，而是因为这个 writable snapshot 默认会随容器 cleanup。

本次改造的核心就是把这个“临时 writable snapshot”变成“可命名、可复用、受保护的 persistent snapshot”。

## 默认行为与改造后行为

默认行为：

```text
image layer -> 新建临时 writable snapshot(containerID) -> 删除容器时 cleanup
```

改造后：

```text
普通容器:
  image layer -> 新建临时 writable snapshot -> 删除容器时 cleanup

持久容器:
  image layer -> 创建或复用固定 writable snapshot -> 删除容器时保留
```

## 用户接口

用户通过 Pod annotation 声明启用持久层：

```yaml
metadata:
  annotations:
    persist.containerd.dev/enabled: "true"
    persist.containerd.dev/id: "demo-rootfs-1"
```

说明：

- `persist.containerd.dev/enabled`
  - 是否启用持久 writable layer
- `persist.containerd.dev/id`
  - 持久层逻辑标识
- `persist.containerd.dev/image-digest`
  - 可选，仅用于校验；不会覆盖真实镜像身份

## 代码修改概览

### 1. 新增 annotation 定义

文件：

- [internal/cri/annotations/annotations.go](/Users/dxy/Desktop/app/containerd/internal/cri/annotations/annotations.go)

新增：

- `persist.containerd.dev/enabled`
- `persist.containerd.dev/id`
- `persist.containerd.dev/image-digest`

### 2. 创建容器时识别 persistent snapshot

文件：

- [internal/cri/server/container_create.go](/Users/dxy/Desktop/app/containerd/internal/cri/server/container_create.go)
- [internal/cri/server/persistent_snapshot.go](/Users/dxy/Desktop/app/containerd/internal/cri/server/persistent_snapshot.go)

逻辑：

1. 从 Pod annotation 解析持久层配置
2. 如果未启用持久化，继续走原逻辑
3. 如果启用持久化，则生成稳定 snapshot key，并记录到 container metadata

当前 snapshot key 形式为：

```text
persist/<persist-id>-<container-name>
```

例如：

```text
persist/demo-rootfs-1-box
```

这样做是为了避免一个多容器 Pod 内多个容器共享同一个 writable layer。

### 3. 新增 persistent snapshot 创建/复用逻辑

文件：

- [internal/cri/opts/persistent_snapshot.go](/Users/dxy/Desktop/app/containerd/internal/cri/opts/persistent_snapshot.go)

新增 `WithPersistentSnapshot(...)`，逻辑如下：

- snapshot 不存在：
  - 基于镜像 rootfs 创建新的 writable snapshot
- snapshot 已存在：
  - 直接复用该 snapshot

也就是说：

- 首次运行时是“创建”
- 后续重建时是“复用”

### 4. 删除容器时跳过持久 snapshot cleanup

文件：

- [internal/cri/server/container_remove.go](/Users/dxy/Desktop/app/containerd/internal/cri/server/container_remove.go)

逻辑：

- 普通容器：
  - 继续 `WithSnapshotCleanup`
- 持久容器：
  - 只删除 container/task metadata
  - 不删除对应 writable snapshot

同时，在创建失败回滚路径中，也对持久容器跳过 snapshot cleanup，避免已有持久层被误删。

### 5. 将持久层信息存入 container metadata

文件：

- [internal/cri/store/container/metadata.go](/Users/dxy/Desktop/app/containerd/internal/cri/store/container/metadata.go)

新增持久层元数据，记录：

- persist id
- snapshot key
- image ref

这样在 RemoveContainer 时不需要重新依赖 Pod annotation，就可以知道当前容器是否绑定了 persistent snapshot。

## 关键保护措施

为了让方案可用且不容易出事故，本次实现额外补了几项保护。

### 1. 不允许 annotation 覆盖真实镜像 digest

`persist.containerd.dev/image-digest` 如果存在，只用于一致性校验。

真实镜像身份始终使用 containerd 解析出的 `image.ID`。

这样可以避免用户通过伪造 annotation，把旧 writable layer 误绑定到另一个镜像上。

### 2. 防止多容器 Pod 共用同一个 writable layer

如果 snapshot key 只用 `persist/<id>`，则一个多容器 Pod 中所有容器都可能共享同一个 writable snapshot。

这会带来：

- 不同镜像时创建失败
- 相同镜像时 rootfs 双写污染

因此当前实现将 `container-name` 拼入 snapshot key。

### 3. 防止同一 persistent snapshot 被并发双写

当前实现会在创建容器前扫描本地 container store：

- 如果某个 persistent snapshot key 已被其他非退出容器占用
- 则拒绝新的容器创建

当前语义等价于：

```text
single-node + single-writer
```

### 4. 通过 label 保护 snapshot 不被 GC 清理

持久 snapshot 创建时会附加这些 label：

- `persist.containerd.dev/enabled=true`
- `persist.containerd.dev/id=<id>`
- `persist.containerd.dev/image-digest=<real image id>`
- `containerd.io/gc.root=true`

这样 containerd GC 不会将其当作无主临时 snapshot 清理掉。

## 运行效果

当前已经验证通过的行为如下：

1. 创建带持久 annotation 的 Pod
2. 在容器 rootfs 中写入文件，例如：

```sh
echo hello > /root/persist-marker
```

3. 删除 Pod，由 Deployment 自动重建
4. 在新 Pod 中再次读取：

```sh
cat /root/persist-marker
```

结果仍然为：

```text
hello
```

这说明新 Pod 复用的是同一个 writable snapshot，而不是新建的临时层。

## 方案边界

当前这套实现可以稳定支持：

- 删除 Pod 保留 writable layer
- 重新启动时继续复用原 writable layer
- 同镜像 digest 下复用
- 同节点单 writer 模式

但仍有几个明确边界：

### 1. 这是节点本地持久化，不是跨节点持久化

persistent snapshot 是节点本地数据。

如果 Pod 被调度到其他节点，默认不会自动拿到旧的 writable layer，除非额外实现：

- 节点绑定
- 导出/导入
- 跨节点迁移

### 2. 不适合由 CRI 自己决定最终删除时机

当前 CRI 侧只适合做 retain：

- 删除容器时保留 snapshot

但“什么时候真正删除这个 persistent snapshot”不应完全由 CRI 决定，因为 CRI 看不到：

- 用户删的是 Pod 还是 Deployment
- 是滚动更新还是彻底删除应用

更合理的做法是让上层 controller 或 node-agent 根据 workload 生命周期决定最终删除。

### 3. 当前没有完整的容量治理

本次改造解决的是“保留/复用 writable layer”，不包含：

- per-snapshot quota
- 自动扩容
- TTL
- Deployment 删除后的自动回收

这些能力需要在后续控制面中补齐。

## 关于 snapshot 目录

需要区分两层概念：

### 1. snapshot key 是稳定的

当前实现中，持久层逻辑标识是：

```text
persist/<persist-id>-<container-name>
```

例如：

```text
persist/demo-rootfs-1-box
```

这个 key 是稳定且可依赖的。

### 2. 磁盘上的实际目录路径不应假定固定

真正落盘路径由 snapshotter 决定，不同后端实现不同：

- overlayfs
- native
- devmapper
- blockfile

即使是同一个 snapshotter，物理目录通常也会通过 metadata 映射为内部 ID，而不是直接使用 snapshot key 作为目录名。

因此：

- 业务逻辑应依赖 snapshot key
- 不应直接依赖宿主机上的某个固定目录结构

## 删除方式

Kubernetes 原生不会直接管理 containerd snapshot key。

因此，像下面这样的 key：

```text
persist/demo-rootfs-1-box
```

不是 Kubernetes API 资源，而是 containerd 节点本地对象。

当前可行的删除方式是：

```sh
sudo ctr --address /run/containerd/containerd.sock --namespace k8s.io snapshots rm persist/demo-rootfs-1-box
```

注意：

- 必须在对应节点执行
- 必须确保没有运行中的容器仍在使用该 snapshot

## 关于动态扩容

当前方案本身不包含动态扩容。

containerd 没有统一的通用 snapshot resize API，不同 snapshotter 的能力差异很大：

- `overlayfs`
  - 更适合当前的持久 rootfs 改造
  - 不适合作为第一版动态扩容实现基础
- `devmapper` / `blockfile`
  - 更接近“每个 snapshot 有独立容量”的模型
  - 更适合做“只增不减”的扩容设计

因此，如果未来需要：

- 初始容量
- 扩容但不缩容

更合理的演进方向是：

1. 当前 persistent snapshot 逻辑保持不变
2. 控制面增加 size 元数据
3. 由 node-agent 在合适的 snapshotter 上执行扩容动作

## 推荐的后续演进

如果要把该能力从验证方案演进为正式能力，建议继续补齐：

1. `PersistentRootFS` 之类的 CRD 或等价资源
2. controller / node-agent
3. `Retain/Delete/TTL` 回收策略
4. 节点绑定与跨节点迁移能力
5. 容量治理和监控

## 一句话总结

本方案不是改 Kubernetes Volume，也不是持久化镜像 layer，而是在 containerd CRI 中把容器的 writable layer 从“按 containerID 创建、随容器删除的临时 snapshot”，改成“按稳定 key 创建/复用、删除容器时保留、由 label 保护的 persistent snapshot”。

其结果是：在同节点、同持久 id、同镜像 digest 的前提下，容器删除重建后仍可继续使用之前的 rootfs 修改结果。
