# 《containerd 原理剖析与实战》第 6 章伴读

> **适配版本：containerd 2.2.5**
> **对应原书章节：第 6 章 containerd 与容器存储**
> **源码基线：用户提供的 `containerd-2.2.5` 源码包**

---

## 阅读说明

containerd 存储最容易混淆的三个词是：

```text
Content      镜像原始内容：manifest、config、layer blob
Image        指向目标 descriptor 的元数据对象
Snapshot     解包后的文件系统状态与容器可写层
```

先记住一句话：

> 镜像不是一个目录；容器 rootfs 也不是直接挂载 content blob。

典型链路：

```text
registry
  │ pull
  ▼
Content Store：压缩 blob
  │ image metadata 指向 manifest/index
  ▼
Image Service
  │ unpack
  ▼
Snapshotter：应用 diff，形成 committed snapshots
  │ new container
  ▼
Active snapshot：容器可写层
  │ mounts
  ▼
shim/runc 挂载为容器 rootfs
```

本章会严格区分：

- 逻辑 API；
- metadata 记录；
- 磁盘实际文件；
- snapshotter 的实现差异。

---

## 6.1 containerd 中的数据存储

### 6.1.1 理解容器镜像

#### 6.1.1.1 OCI 镜像不是“一个 tar 包”

一个 OCI/Docker 镜像通常由 descriptor 图组成：

```text
Image name
  ↓
index / manifest descriptor
  ├── config descriptor
  └── layer descriptor 1
      layer descriptor 2
      layer descriptor 3
```

每个 descriptor 至少包含：

```text
mediaType
digest
size
annotations（可选）
platform（index 场景）
```

digest 是内容寻址的核心：

```text
sha256:<hash>
```

只要内容改变，digest 就改变。相同 blob 无论被多少镜像引用，底层 content store 都可只保存一份。

#### 6.1.1.2 压缩层与解包层

registry 下载到 content store 的 layer blob 常是压缩 tar：

```text
application/vnd.oci.image.layer.v1.tar+gzip
```

容器不能直接把 gzip blob 当普通 rootfs 使用。unpack 阶段会：

1. 解压 layer；
2. 解释 whiteout；
3. 把文件变更应用到 active snapshot；
4. commit 为 committed snapshot；
5. 继续以下一层为 parent。

因此磁盘可能同时存在：

```text
content 中的压缩 blob
snapshotter 中的解包文件
```

这也是“镜像大小”和“节点实际磁盘占用”不相等的重要原因。

#### 6.1.1.3 DiffID、Digest 与 ChainID

三者用途不同：

```text
Digest   对压缩 blob 求摘要，registry/content 使用
DiffID   对解压后的 layer tar 流求摘要
ChainID  根据父 ChainID + 当前 DiffID 递归计算，标识层链
```

示意：

```text
chain1 = diffID1
chain2 = digest(chain1 + " " + diffID2)
chain3 = digest(chain2 + " " + diffID3)
```

snapshotter committed snapshot 常以 chain ID 关联解包结果。相同层内容但父链不同，语义上是不同的 rootfs 状态。

#### 6.1.1.4 Image metadata 的作用

containerd 的 Image 对象主要包含：

```text
Name       例如 docker.io/library/nginx:latest
Target     manifest/index descriptor
Labels     GC、管理与扩展信息
CreatedAt/UpdatedAt
```

它不是 blob 本体。删除一个 image name 只是在 metadata 中删除引用；blob 是否立即删除取决于：

- 是否还有其他 image/lease/container 引用；
- GC 是否运行；
- snapshot 是否仍使用；
- content sharing policy。

#### 6.1.1.5 多架构镜像

多架构镜像 target 常指向 index：

```text
index
├── linux/amd64 manifest
├── linux/arm64 manifest
└── windows/amd64 manifest
```

pull 时可能先保存 index 和多个 descriptor metadata，unpack 时再根据 platform 选择具体 manifest。CRI image plugin 还可按 runtime 配置 platform/snapshotter 映射。

命令观察：

```bash
ctr images inspect docker.io/library/busybox:latest
ctr content tree <target-digest>
ctr content get <digest> | jq .
```

---

### 6.1.2 containerd 中的存储目录

#### 6.1.2.1 `root` 与 `state`

Linux 默认：

```text
root  = /var/lib/containerd
state = /run/containerd
```

区别：

| 目录 | 生命周期 | 典型内容 |
|---|---|---|
| `/var/lib/containerd` | 持久化 | content、meta.db、snapshotter 数据、插件持久状态 |
| `/run/containerd` | 易失 | socket、shim bundle、PID、FIFO、运行时临时状态 |

不要通过备份 `/run/containerd` 恢复容器数据；也不要在 daemon 运行时直接删除 `/var/lib/containerd` 的子目录。

#### 6.1.2.2 插件目录是如何生成的

server 初始化插件时，根据插件 URI 给每个插件分配 root/state：

```text
<root>/<plugin-type>.<plugin-id>
<state>/<plugin-type>.<plugin-id>
```

所以常见目录类似：

```text
/var/lib/containerd/
├── io.containerd.content.v1.content/
├── io.containerd.metadata.v1.bolt/
├── io.containerd.snapshotter.v1.overlayfs/
├── io.containerd.snapshotter.v1.native/
└── ...
```

实际启用哪些目录取决于插件、平台和配置。

#### 6.1.2.3 `meta.db`

metadata 插件：

```text
Type: io.containerd.metadata.v1
ID:   bolt
```

源码 `plugins/metadata/plugin.go` 明确创建：

```text
<metadata-plugin-root>/meta.db
```

因此默认通常是：

```text
/var/lib/containerd/io.containerd.metadata.v1.bolt/meta.db
```

注意文件名是 `meta.db`，不是 `metadata.db`。

它记录的主要是关系和元数据，例如：

- namespaces；
- image records；
- container records；
- content metadata/reference；
- snapshot metadata view；
- leases；
- GC labels/roots；
- sandbox 等服务对象。

大体积 layer 内容不直接塞进 BoltDB。

#### 6.1.2.4 不要离线猜目录结构

snapshotter 的内部目录不是稳定公共 API。即使能看到：

```text
snapshots/<numeric-id>/fs
```

数字 ID 与 snapshot key 的映射仍可能在 snapshotter metadata DB 中。直接按目录名删除会破坏一致性。

正确查看方式：

```bash
ctr snapshots list
ctr snapshots info <name>
ctr content list
ctr images list
ctr containers list
```

底层目录只用于验证，不应用作管理接口。

#### 6.1.2.5 迁移与备份思路

一致性备份至少要考虑：

```text
metadata DB
content store
snapshotter 数据
插件专属状态
运行中 task 的一致性
```

只复制 `meta.db` 会得到指向不存在 blob/snapshot 的记录；只复制 snapshot 目录会失去命名关系和镜像 metadata。

更稳妥的迁移通常是：

- 通过 registry/OCI archive 重新分发镜像；
- 停止写入后做一致性文件系统快照；
- 使用发行方或 snapshotter 提供的迁移方案；
- 对有状态业务备份应用数据卷，而不是把容器可写层当持久卷。

---

### 6.1.3 containerd 中的镜像存储

#### 6.1.3.1 Image、Content、Snapshot 的关系

```text
Image record
  │ Target descriptor
  ▼
Content graph
  │ unpack
  ▼
Snapshot chain
```

三套对象可独立存在：

- content 已下载，但尚无 image name；
- image metadata 已存在，但尚未 unpack；
- snapshot 已 unpack，但 image name 后来被删除；
- container 引用 active snapshot，但对应 tag 已变化。

#### 6.1.3.2 Pull 不一定等于 Unpack

containerd client 可选择：

```go
client.Pull(ctx, ref)
```

或：

```go
client.Pull(ctx, ref, containerd.WithPullUnpack)
```

前者主要拉取 content 和创建 image metadata，后者还解包到默认 snapshotter。

命令层面也要确认是否指定 unpack。镜像列表中的 `UNPACKED` 状态可能按 snapshotter/platform 区分。

#### 6.1.3.3 Unpack 的核心过程

可简化为：

```text
遍历 rootfs layers
  ↓
根据 DiffID 计算 chain ID
  ↓
检查对应 committed snapshot 是否已有
  ├─ 有：复用
  └─ 无：
      Prepare(active-key, parent-chain)
        ↓
      mount active snapshot
        ↓
      Diff Apply：解包并应用 whiteout
        ↓
      Commit(chainID, active-key)
```

这解释了为何相同基础层只需解包一次。

#### 6.1.3.4 容器可写层

创建容器时：

```go
containerd.WithNewSnapshot(snapshotKey, image)
```

会以镜像最终 committed snapshot 为 parent，创建一个 active snapshot：

```text
image committed chain
        ↓ parent
container active snapshot
```

容器写入只进入 active 层，不修改镜像 committed 层。

删除 container 时若带 snapshot cleanup，才会删除对应 active snapshot。单纯删除 Task 并不一定删除 Container 或 snapshot。

#### 6.1.3.5 Volumes 与可写层不同

容器 writable snapshot：

- 生命周期通常跟容器；
- 适合临时写入；
- 不宜承载数据库持久数据；
- snapshotter 语义和性能取决于实现。

Kubernetes volume/PVC：

- 独立于镜像层；
- 由 kubelet/CSI 挂载；
- 有独立生命周期和备份策略。

不要用 `du` 容器 rootfs 代替卷容量监控。

---

### 6.1.4 containerd 中的 content

#### 6.1.4.1 Content Store 接口

`core/content` 的抽象围绕：

```text
ReaderAt      按 descriptor 读取
Writer        可恢复 ingest 写入
Info/Walk     查询 blob
Update        更新 labels
Delete        删除内容
Status/ListStatuses/Abort  管理未完成 ingest
```

content store 的核心特点：

```text
immutable blob + digest addressing
```

一个 digest 对应唯一内容。写入完成后 commit 时会校验 expected digest/size。

#### 6.1.4.2 本地 Content 插件

插件注册：

```text
Type: io.containerd.content.v1
ID:   content
```

源码入口：

```text
plugins/content/local/plugin/plugin.go
core/content/local/
```

本地存储大致分：

```text
blobs/<algorithm>/<encoded-digest>
ingest/<ref>/...
```

具体目录以实现为准。ingest 用于未完成下载，commit 后进入不可变 blob 区。

#### 6.1.4.3 可恢复写入

下载大层时，Writer Status 包含：

```text
Ref
Offset
Total
StartedAt
UpdatedAt
```

客户端可根据 offset 续传。写入流程：

```text
OpenWriter(ref, expected)
  ↓
Write chunks
  ↓
Commit(size, digest)
  ↓
校验并转为 blob
```

同一 expected digest 已存在时，content store 可避免重复下载或直接返回 already exists 语义。

#### 6.1.4.4 Content labels 与 GC

containerd GC 使用 labels 构造对象引用图。常见语义是：

```text
containerd.io/gc.ref.content.*
containerd.io/gc.ref.snapshot.*
```

例如 image target 指向 manifest，manifest metadata 再指向 config 与 layers。只在磁盘上“看起来没人用”并不表示可以直接删除；GC 依据 metadata/lease/labels 判断可达性。

#### 6.1.4.5 Namespace sharing policy

metadata 插件配置：

```toml
[plugins."io.containerd.metadata.v1.bolt"]
  content_sharing_policy = "shared"
  no_sync = false
```

两种 policy：

```text
shared
  已知 digest 时，blob 可在 namespace 间共享底层内容和访问收益

isolated
  客户端必须证明拥有内容，隔离 namespace metadata 访问
```

两种都可共享底层 backing data，但授权语义不同。默认 `shared` 减少跨 namespace 重复下载，但知道 digest 的 namespace 更容易获取已有 blob。

`no_sync=true` 会禁用部分 bbolt 同步以提升性能，源码明确警告崩溃时存在数据丢失风险。它不应作为“磁盘慢”的第一修复手段。

#### 6.1.4.6 常用 content 命令

```bash
ctr content list
ctr content info <digest>
ctr content get <digest>
ctr content tree <digest>
ctr content active
ctr content prune references
```

不要对二进制 layer 直接在终端 `cat`；manifest/config 可 pipe 给 `jq`，layer 可导出到文件后检查 media type。

---

### 6.1.5 containerd 中的 snapshot

#### 6.1.5.1 Snapshotter 接口

源码：

```text
core/snapshots/snapshotter.go
```

主要方法：

```go
Stat(ctx, key)
Update(ctx, info, fieldpaths...)
Usage(ctx, key)
Mounts(ctx, key)
Prepare(ctx, key, parent, opts...)
View(ctx, key, parent, opts...)
Commit(ctx, name, key, opts...)
Remove(ctx, key)
Walk(ctx, fn, filters...)
Close()
```

这是 containerd snapshotter 插件必须实现的合同。

#### 6.1.5.2 三种 Kind

```text
KindActive
  可写，通常由 Prepare 创建

KindView
  只读临时视图，由 View 创建

KindCommitted
  不可变，可作为 parent，由 Commit 产生
```

状态转换：

```text
Prepare(key, parent)
  → Active(key)
  → 写入文件变化
  → Commit(name, key)
  → Committed(name)
```

Commit 后原 active key 消失，换成 committed name。

#### 6.1.5.3 `key` 与 `name`

`Prepare` 的 `key` 是 active snapshot 的事务性标识；`Commit` 的 `name` 是最终不可变 snapshot 名。

```go
Prepare("extract-random", parent)
Commit(chainID, "extract-random")
```

这种设计允许并发解包时先用唯一 active key，成功后再原子提交到确定的 chain ID。如果目标 committed snapshot 已被别人先提交，可丢弃临时 active 并复用已有结果。

#### 6.1.5.4 Mounts 返回的是描述，不一定已经挂载

`Prepare`/`Mounts` 返回：

```go
[]mount.Mount
```

每个 Mount 包含：

```text
Type
Source
Options
```

snapshotter 通常描述“应如何挂载”，调用者再执行实际 mount。不同 snapshotter 可返回：

- bind；
- overlay；
- block device；
- fuse；
- remote filesystem。

因此不能假设每个 snapshot 都对应一个可直接进入的 `fs` 目录。

#### 6.1.5.5 Usage 是本层使用量

源码注释强调：

> Usage 只统计 snapshot 自身消费的资源，不包含 parent。

所以一个容器 rootfs 总占用不能简单等于 active snapshot Usage；要视存储实现和父链共享计算。

#### 6.1.5.6 删除的异步性

`Remove` 从 metadata 中移除 snapshot 后，实际文件清理可能异步发生。overlayfs 有异步 remove 选项，其他 snapshotter 也可能把资源回收延后。

因此看到 snapshot list 已无对象，但磁盘空间尚未立即下降，不一定是泄漏；需要结合 GC、异步清理队列和打开文件句柄判断。

---

## 6.2 containerd 镜像存储插件 snapshotter

### 6.2.1 Docker 中的 graphdriver

传统 Docker graphdriver 把镜像层、容器可写层和 mount 管理封装在 Docker Engine 内部，典型实现有 overlay2、devicemapper、aufs 等。

其常见抽象围绕：

```text
Create layer
Get/Mount layer
Put/Unmount layer
Remove layer
Diff/ApplyDiff
```

graphdriver 与 Docker image/layer store 关系紧密。

#### 6.2.1.1 为什么 containerd 不直接沿用 graphdriver API

containerd 需要：

- 独立 content store；
- 独立 diff service；
- 可插拔 snapshotter；
- 支持非目录型 rootfs；
- 支持 remote/lazy、块设备、虚拟机 runtime；
- 用标准 mount descriptors 与 runtime 解耦。

因此采用 Snapshotter + Diff + Content 的分层，而不是把所有职责塞在一个 storage driver 中。

---

### 6.2.2 graphdriver 与 snapshotter

| 维度 | graphdriver | snapshotter |
|---|---|---|
| 所属生态 | 传统 Docker Engine 内部 | containerd Core 插件接口 |
| 原始 blob 管理 | 常与 Docker image store 绑定 | 由 Content Store 独立负责 |
| 层应用/导出 | driver 常承担较多职责 | Diff Service 与 snapshotter 分离 |
| mount 表达 | driver-specific | 标准 `[]mount.Mount` |
| 状态模型 | layer/container layer | active/view/committed snapshot |
| 扩展形态 | 以 Docker driver 为中心 | overlay、native、block、remote、VM 等 |

不要把 snapshotter 简单翻译成“containerd 的 graphdriver 新名字”。它们解决相似问题，但接口边界不同。

#### 6.2.2.1 Diff Service 的位置

```text
Content blob
  │
  │ Diff Apply
  ▼
Mounted active snapshot
```

反向：

```text
parent mount + child mount
  │
  │ Diff Compare
  ▼
OCI layer tar blob
```

snapshotter 管文件系统状态，diff service 管变化集的应用和生成。overlay snapshotter 不必自己理解所有 OCI tar/whiteout 语义。

---

### 6.2.3 snapshotter 概述

#### 6.2.3.1 插件注册

containerd 插件类型：

```text
io.containerd.snapshotter.v1
```

常见 ID：

```text
overlayfs
native
devmapper
btrfs
blockfile
erofs
windows
```

查看：

```bash
ctr plugins list | grep snapshotter
```

只有状态 `ok` 的插件才能使用。

#### 6.2.3.2 默认 snapshotter

Linux 默认：

```text
overlayfs
```

Native API 客户端可在操作时指定 snapshotter；CRI image/runtime 配置也能指定全局或 per-runtime snapshotter。

同一镜像可在多个 snapshotter 中分别 unpack：

```text
busybox + overlayfs → 一套 snapshot chain
busybox + native    → 另一套 snapshot chain
```

Content blob 可以共享，但解包数据不共享。

#### 6.2.3.3 Snapshot labels

源码规定只有前缀：

```text
containerd.io/snapshot/
```

的 labels 才会在 Prepare/View/Commit 路径中继承给 snapshotter。remote snapshotter 常使用 labels 获取 layer digest、URL 或 image reference。

#### 6.2.3.4 Snapshotter 与 namespace

metadata DB 为 namespace 提供逻辑视图；snapshotter 后端的实际数据可被共享。不要假设切换 containerd namespace 就生成一份物理 snapshot 副本。

---

### 6.2.4 containerd 中如何使用 snapshotter

#### 6.2.4.1 拉取并解包

```bash
ctr images pull --snapshotter overlayfs docker.io/library/busybox:latest
```

查看：

```bash
ctr snapshots --snapshotter overlayfs list
ctr images check
```

#### 6.2.4.2 创建 active snapshot

```bash
ctr snapshots --snapshotter overlayfs prepare demo-active <parent>
ctr snapshots --snapshotter overlayfs mounts demo-active
```

`mounts` 输出可交给 mount helper。实验完成：

```bash
ctr snapshots --snapshotter overlayfs remove demo-active
```

#### 6.2.4.3 View

```bash
ctr snapshots --snapshotter overlayfs view demo-view <parent>
ctr snapshots --snapshotter overlayfs mounts demo-view
ctr snapshots --snapshotter overlayfs remove demo-view
```

View 用于只读访问，不应在其中写入并期望 Commit。

#### 6.2.4.4 容器创建

Go client：

```go
container, err := client.NewContainer(
    ctx,
    "demo",
    containerd.WithImage(image),
    containerd.WithNewSnapshot("demo-snapshot", image),
    containerd.WithNewSpec(oci.WithImageConfig(image)),
)
```

`WithNewSnapshot` 把 snapshot 生命周期与 container metadata 建立关联；创建 Task 时 runtime 取得 rootfs mounts。

#### 6.2.4.5 删除与清理

```go
container.Delete(ctx, containerd.WithSnapshotCleanup)
```

若不带 cleanup，container metadata 删除后 snapshot 可能保留。生产代码必须设计幂等清理，尤其在 NewContainer 成功、NewTask 失败的半成品路径。

---

## 6.3 containerd 支持的 snapshotter

### 6.3.1 native snapshotter

#### 6.3.1.1 原理

源码：

```text
plugins/snapshots/native/native.go
plugins/snapshots/native/plugin/plugin.go
```

native snapshotter 的核心思想是：

```text
新 snapshot = 复制 parent 文件树 + 在独立目录中修改
```

mount 通常返回 bind mount。它不依赖 overlayfs 特性，容易理解且兼容性好。

#### 6.3.1.2 Prepare

有 parent 时，Prepare 需要把父 snapshot 内容复制到新 active 目录。没有 CoW 文件系统辅助时，时间和空间与父树大小相关。

这意味着多层镜像可能产生大量重复文件，性能和磁盘效率通常不如 overlayfs。

#### 6.3.1.3 适用场景

- 学习 Snapshotter API；
- overlayfs 不可用的环境；
- 小规模、低性能要求；
- 验证问题是否由 overlayfs 引起；
- 底层文件系统本身提供高效 reflink 时可能受益。

#### 6.3.1.4 配置与验证

```bash
ctr plugins list | grep native
ctr images pull --snapshotter native docker.io/library/busybox:latest
ctr snapshots --snapshotter native list
```

不要在磁盘空间紧张节点同时给大镜像在 native 和 overlayfs 解包。

---

### 6.3.2 overlayfs snapshotter

#### 6.3.2.1 原理

OverlayFS 合并：

```text
lowerdir = 一个或多个只读父层
upperdir = 当前 active 层写入
workdir  = overlay 工作目录
merged   = 容器看到的联合视图
```

mount 示例：

```text
type=overlay
options=
  workdir=.../work
  upperdir=.../fs
  lowerdir=.../fs:.../fs
```

容器读取：从 upper 向 lower 查找。容器写入父层文件时触发 copy-up，删除父层文件以 whiteout/opaque 语义表示。

#### 6.3.2.2 源码位置

```text
plugins/snapshots/overlay/overlay.go
plugins/snapshots/overlay/plugin/plugin.go
plugins/snapshots/overlay/metastore.go
```

插件 ID：

```text
overlayfs
```

#### 6.3.2.3 为什么层链可能被压缩/重排为 lowerdirs

Snapshotter metadata 保存 parent 关系，Mounts 时把 committed parent chain 展开为 OverlayFS lowerdir 列表。active 层只需要一个 upperdir/workdir。

```text
active upper
  + parent3 lower
  + parent2 lower
  + parent1 lower
```

这避免每一层都重新复制完整父目录。

#### 6.3.2.4 内核和文件系统要求

需要：

- 内核支持 overlay；
- backing filesystem 支持所需 xattr/d_type；
- rootless 时 user namespace 与 overlay/fuse 兼容；
- SELinux、idmapped mount 等组合满足内核版本要求。

检查：

```bash
modprobe overlay
cat /proc/filesystems | grep overlay
ctr plugins list | grep overlayfs
```

插件为 error 时，详细原因看 containerd 日志。

#### 6.3.2.5 配置项

2.2.5 overlay 插件支持的配置以生成的默认配置为准，常见方向包括：

- `root_path`；
- `upperdir_label`；
- `sync_remove`；
- `slow_chown`；
- `mount_options`。

不同平台/构建可能不同，优先执行：

```bash
containerd config default | sed -n '/snapshotter.v1.overlayfs/,/^\[/p'
```

`sync_remove=false` 时，Remove 可先移除 metadata，再异步删除目录，提高前台响应，但空间回收滞后。

#### 6.3.2.6 性能与反直觉点

OverlayFS 节省空间并不意味着所有操作都快：

- 首次修改大文件会 copy-up；
- 大量小文件和 metadata 操作可能昂贵；
- 深层 lowerdir 增加查找复杂度；
- `du` 在 merged view 中可能重复理解共享层；
- 打开的已删除文件仍占空间；
- 日志和数据库写在 writable layer 会放大问题。

有状态数据应使用 volume，不应依赖容器 upperdir 性能。

---

### 6.3.3 devmapper snapshotter

#### 6.3.3.1 原理

devmapper snapshotter 使用 device-mapper thin provisioning：

```text
thin pool
├── base/parent thin device
├── child snapshot thin device
└── container active thin device
```

每个 snapshot 更接近块设备，而不是普通目录。Mounts 返回设备及文件系统挂载信息。

#### 6.3.3.2 源码位置

```text
plugins/snapshots/devmapper/
plugins/snapshots/devmapper/plugin/plugin.go
plugins/snapshots/devmapper/snapshotter.go
plugins/snapshots/devmapper/config.go
```

插件 ID：

```text
devmapper
```

#### 6.3.3.3 与旧 Docker devicemapper 的关系

都利用 device-mapper thin pool，但 containerd devmapper 实现遵循 Snapshotter API。不能直接把 Docker graphdriver 的配置、metadata 和运维命令原样套用。

#### 6.3.3.4 适用场景

- Kata/VM runtime 需要块设备 rootfs；
- 希望避免 overlay 文件级 copy-up；
- 底层有成熟 LVM/device-mapper 管理；
- 需要块级快照隔离。

代价：

- thin pool 规划复杂；
- data/metadata 空间必须监控；
- 设备泄漏与 transaction 恢复更难；
- 文件系统格式化和 mount 有成本；
- 配置错误可能影响整个 pool。

#### 6.3.3.5 配置概念

常见配置涉及：

```text
pool_name
root_path
base_image_size
fs_type
fs_options
async_remove
file_system_type / discard behavior
```

准确字段以：

```bash
containerd config default | sed -n '/snapshotter.v1.devmapper/,/^\[/p'
```

和 `plugins/snapshots/devmapper/config.go` 为准。

#### 6.3.3.6 thin pool 风险

必须同时监控：

```text
Data%     数据块使用率
Meta%     thin metadata 使用率
```

metadata 满可能比数据满更危险，会导致 snapshot 创建、写入或删除失败。删除 snapshot 也不一定立刻让 pool 指标下降，取决于 deferred/async remove 和 discard。

#### 6.3.3.7 per-runtime 使用

CRI 可为特定 runtime 指定：

```toml
[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  snapshotter = "devmapper"
```

普通 runc 仍用 overlayfs，实现一台节点多存储后端。

---

## 6.4 其他值得认识的 snapshotter

containerd 2.2.5 源码树中还能看到：

| Snapshotter | 思路 |
|---|---|
| `btrfs` | 利用 Btrfs subvolume/snapshot |
| `blockfile` | 以块文件承载文件系统快照 |
| `erofs` | 只读 EROFS 镜像/快照能力 |
| Windows snapshotters | WCOW/CIMFS 等 Windows 容器存储 |
| Proxy snapshotter | 通过 gRPC 连接外部 snapshotter |

生态中还有 stargz、nydus、overlaybd 等 remote/lazy snapshotter。它们常通过 proxy plugin 接入，不一定在 containerd 主仓库源码中。

lazy pulling 的关键变化：

```text
传统：先下载完整 layer → 解包 → 启动
远程：先取得 metadata → 按需拉取文件块 → 更快启动
```

代价是运行时读路径依赖远端、缓存和 FUSE/特殊文件系统，故障模型与传统本地 snapshotter 不同。

---

## 6.5 GC、Lease 与“为什么文件还没删”

### 6.5.1 Mark-and-sweep 思维

containerd GC 大致从 roots 出发标记：

```text
images
containers
snapshots
leases
ingest
sandbox
插件注册的 GC roots
```

再沿 labels/reference 标记 content 和 snapshot，最后清理不可达对象。

#### 6.5.1.1 Lease

长操作可创建 lease，把临时 content/snapshot 绑定进去：

```text
pull/unpack in progress
  ↓
lease protects objects
  ↓
成功后建立正式引用
  ↓
删除 lease
```

没有 lease，GC 可能在并发操作中误判临时对象不可达。

命令：

```bash
ctr leases list
ctr leases create --id demo
ctr leases delete demo
```

`ctr` 的 `images pull`、`run` 等命令会自行创建并清理临时 lease；2.2.5 的 `ctr` 没有把已有 lease 注入任意 `images pull` 的全局 `--lease` 参数。需要把一组自定义 content/snapshot 操作固定到指定 lease 时，应使用 Go client，在 `leases.WithLease(ctx, "demo")` 的 context 上调用相应 API。

#### 6.5.1.2 删除 image 后空间不降

可能原因：

1. 其他 tag/image 引用同 blob；
2. container 引用 snapshot；
3. lease 保护对象；
4. blob 尚未 GC；
5. snapshot async remove；
6. 文件被进程打开；
7. 同一层在其他 namespace 可达；
8. `du`/`df` 统计口径不同。

不要直接 `rm -rf blobs`。应先检查对象图。

---

## 6.6 源码阅读路径

### 6.6.1 Content

```text
core/content/
  │ interface
  ▼
core/content/local/
  │ local implementation
  ▼
plugins/content/local/plugin/
  │ plugin registration
  ▼
plugins/services/content/
  │ gRPC service
  ▼
client content APIs / ctr content
```

### 6.6.2 Metadata

```text
plugins/metadata/plugin.go
  ↓
core/metadata/
  ├─ buckets
  ├─ content wrapper
  ├─ image store
  ├─ container store
  ├─ snapshot metadata
  └─ garbage collection
```

### 6.6.3 Snapshot

```text
core/snapshots/snapshotter.go
  ↓
plugins/snapshots/<implementation>
  ↓
plugins/services/snapshots
  ↓
client SnapshotService
  ↓
ctr snapshots / image unpack / container create
```

### 6.6.4 Unpack

搜索：

```bash
rg -n "WithPullUnpack|Unpack\(|Prepare\(|Commit\(" client core plugins
```

沿 image unpack → diff apply → snapshot Prepare/Commit 阅读，能把三种存储对象真正串起来。

---

## 6.7 常见故障分析

### 6.7.1 `failed to extract layer`

可能链：

```text
blob 不完整/校验失败
压缩格式不支持
磁盘/inode 满
snapshot mount 失败
whiteout/xattr 不支持
SELinux label 失败
Diff Apply 超时或 I/O error
```

需要同时看 content digest、snapshotter 状态和内核日志。

### 6.7.2 `snapshot ... already exists`

可能是：

- 上次操作半成功留下 active key；
- 并发 unpack 同一 chain；
- container ID/snapshot key 重用；
- metadata 与后端状态不一致。

先：

```bash
ctr snapshots --snapshotter <name> info <key>
ctr snapshots --snapshotter <name> list
```

不要直接删后端数字目录。

### 6.7.3 overlay mount `invalid argument`

常见方向：

- backing filesystem 不支持；
- lowerdir/upperdir/workdir 不同文件系统；
- mount option 与内核不兼容；
- lowerdir 层数/路径长度；
- user namespace/idmap；
- SELinux/context；
- overlay 模块缺失。

检查：

```bash
journalctl -k
ctr plugins list | grep overlay
findmnt -T /var/lib/containerd
```

### 6.7.4 `meta.db` 锁住

bbolt 通过文件锁防止多个写者。第二个 containerd 指向同一 root 时可能一直等待或超时。不要启动两个 daemon 共用 `/var/lib/containerd`。

源码为 bolt open 配置了等待和日志告警。强行删除 lock 或复制运行中的 DB 都可能破坏一致性。

### 6.7.5 devmapper pool full

表现可能是：

- Prepare 失败；
- 文件系统 I/O error；
- task 启动失败；
- snapshot remove 卡住；
- kubelet DiskPressure。

应查 device-mapper/LVM 状态，而不仅看 `/var/lib/containerd` 所在普通文件系统 `df`。

---

## 6.8 与 containerd 1.7.1 参考书对照

| 原书内容 | 2.2.5 阅读补充 |
|---|---|
| 镜像层存在某个目录 | 先区分 content blob、image metadata、snapshot unpack |
| Docker graphdriver 类比 snapshotter | 强调 Content/Diff/Snapshot 分层差异 |
| metadata 数据库 | 默认文件准确为 `meta.db` |
| image pull | 区分 pull 与 unpack，关注 Transfer Service |
| 只有 native/overlay/devmapper | 2.2.5 主仓库还有 blockfile、erofs 等，并支持 proxy snapshotter |
| 删除镜像即可释放空间 | 还受引用图、lease、GC、async remove 影响 |
| snapshot 是一个目录 | API 返回 mounts，块设备/remote snapshotter 不一定是普通目录 |

---

## 6.9 本章实验

### 实验一：观察 Pull 与 Unpack

```bash
ctr images pull --snapshotter overlayfs docker.io/library/busybox:latest
ctr images check
ctr content list
ctr snapshots --snapshotter overlayfs list
```

记录 manifest、config、layer digest 与 snapshot chain。

### 实验二：同一镜像在两个 snapshotter 解包

实验节点确保 native 插件为 ok：

```bash
ctr images pull --snapshotter native docker.io/library/busybox:latest
ctr snapshots --snapshotter native list
ctr snapshots --snapshotter overlayfs list
```

比较 content 是否复用、snapshot 数据是否各自存在。

### 实验三：观察容器 writable layer

```bash
ctr run -d docker.io/library/busybox:latest write-demo sleep 3600
ctr snapshots --snapshotter overlayfs list
ctr tasks exec --exec-id shell -t write-demo sh
```

容器内写文件后查看 active snapshot Usage。清理：

```bash
ctr tasks kill write-demo
ctr tasks delete write-demo
ctr containers delete write-demo
```

`containers delete` 默认会清理关联 snapshot；只有显式指定 `--keep-snapshot` 才会保留。命令选项仍应以本机 `ctr ... --help` 为准。

### 实验四：查看 metadata 与实际目录

```bash
ctr plugins list --detailed | grep 'io.containerd.metadata.v1.bolt'
find /var/lib/containerd -maxdepth 2 -type f -name 'meta.db' -o -type d | head
```

只观察，不直接修改。

---

## 6.10 本章结论

1. Content、Image、Snapshot 是三套独立但互相引用的对象。
2. content store 保存不可变、按 digest 寻址的原始 blob；metadata DB 保存对象关系。
3. unpack 将 layer diff 应用为 snapshot chain，容器再基于镜像 committed snapshot 创建 active 可写层。
4. Snapshotter API 用 Active/View/Committed 和 Mount descriptors 解耦具体存储实现。
5. native 通过复制文件树实现，overlayfs 通过联合挂载实现，devmapper 通过 thin device 实现。
6. 删除对象不等于立即释放磁盘，必须理解 lease、GC、共享引用和异步清理。
7. 生产数据应放 volume/CSI，不应依赖容器 writable snapshot。
