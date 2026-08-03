# 《containerd 原理剖析与实战》第 5 章伴读

> **适配版本：containerd 2.2.5**
> **对应原书章节：第 5 章 containerd 与容器网络**
> **源码基线：用户提供的 `containerd-2.2.5` 源码包**

---

## 阅读说明

containerd 本身不是一个完整的容器网络方案。它提供容器生命周期和网络命名空间承载能力；在 Kubernetes CRI 场景中，CRI 插件通过 CNI 库调用外部 CNI 插件完成网络配置。

先建立边界：

```text
containerd / CRI
  │ 负责何时配置、给谁配置、保存什么结果
  ▼
go-cni / libcni
  │ 负责读取配置、组装参数、顺序调用插件
  ▼
CNI plugin 可执行文件
  │ 负责创建 veth、bridge、路由、IP、iptables/eBPF 等
  ▼
Linux network namespace / netlink / route / firewall
```

containerd 2.2.5 源码中包含：

- CRI 对 CNI 的调用代码；
- vendored `github.com/containerd/go-cni v1.1.13`；
- vendored `github.com/containernetworking/cni v1.3.0`；
- CNI plugins 依赖中的部分库代码。

但 bridge、host-local、portmap 等完整插件命令属于独立 CNI plugins 项目和外部二进制，不应把它们误认为 containerd 内置网络模块。

---

## 5.1 容器网络接口

### 5.1.1 CNI 概述

#### 5.1.1.1 CNI 解决什么问题

Linux 创建一个新 network namespace 后，里面通常只有 loopback，且 `lo` 可能尚未 UP：

```text
新 netns
└── lo（默认未必可用）
```

要让容器联网，至少需要解决：

1. 在目标 netns 中创建或移动网卡；
2. 分配 IP 地址；
3. 配置路由；
4. 配置 DNS；
5. 连接宿主 bridge、overlay、underlay 或其他数据平面；
6. 容器删除时回收 IP 和清理设备/规则。

CNI 不规定“网络必须怎么实现”，而是规定：

```text
运行时如何调用网络插件
插件如何接收输入
插件如何返回结果
多个插件如何串联
```

因此 bridge、ipvlan、macvlan、Calico、Cilium 等可以拥有不同数据面，但都能通过 CNI 接到运行时。

#### 5.1.1.2 CNI 是可执行文件协议

经典 CNI 插件不是 Go interface 动态链接库，而是外部可执行文件：

```text
/opt/cni/bin/bridge
/opt/cni/bin/host-local
/opt/cni/bin/loopback
/opt/cni/bin/portmap
```

运行时调用时：

- 插件名来自网络配置中的 `type`；
- JSON 配置通过 stdin 传入；
- `CNI_COMMAND`、`CNI_CONTAINERID`、`CNI_NETNS` 等通过环境变量传入；
- stdout 返回 JSON Result；
- stderr 返回诊断信息；
- 退出码表示成功或失败。

这种模型的优点：

- 与运行时进程隔离；
- 插件可用任意语言实现；
- 插件升级无需重新链接 containerd；
- 配置和可执行文件可以独立部署。

代价是每次调用涉及进程启动、环境变量、stdin/stdout 协议和回滚管理。

#### 5.1.1.3 Runtime 与 Plugin 的职责

| 运行时负责 | CNI 插件负责 |
|---|---|
| 创建容器或 Pod 的 netns | 在该 netns 中配置网络 |
| 分配唯一容器 ID | 创建接口、地址、路由、规则 |
| 决定何时 ADD/CHECK/DEL | 按命令执行并返回 Result |
| 提供 ifName、args、capability args | 消费这些参数 |
| 保存必要的结果/缓存 | 正确清理自己创建的资源 |
| 失败时编排回滚 | 保证操作尽可能幂等 |

CNI 不负责创建容器进程，也不负责整个 Pod 生命周期。

#### 5.1.1.4 containerd 2.2.5 的 CNI 依赖链

源码依赖：

```text
github.com/containerd/go-cni v1.1.13
        ↓
github.com/containernetworking/cni v1.3.0
        ↓
libcni + invoke + types
```

CRI Linux 初始化入口：

```text
internal/cri/server/service_linux.go
```

其构造选项包括：

```go
cni.WithMinNetworkCount(...)
cni.WithPluginConfDir(dir)
cni.WithPluginMaxConfNum(...)
cni.WithPluginDir(binDirs)
```

PodSandbox 网络设置入口：

```text
internal/cri/server/sandbox_run.go
```

根据配置选择：

```go
netPlugin.Setup(...)
```

或：

```go
netPlugin.SetupSerially(...)
```

---

### 5.1.2 CNI 配置文件的格式

#### 5.1.2.1 `.conf` 与 `.conflist`

单插件配置 `.conf`：

```json
{
  "cniVersion": "1.0.0",
  "name": "mynet",
  "type": "bridge",
  "bridge": "cni0",
  "isGateway": true,
  "ipMasq": true,
  "ipam": {
    "type": "host-local",
    "subnet": "10.88.0.0/16",
    "routes": [
      {"dst": "0.0.0.0/0"}
    ]
  }
}
```

插件列表 `.conflist`：

```json
{
  "cniVersion": "1.0.0",
  "name": "mynet",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni0",
      "isGateway": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.88.0.0/16"
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
```

两者区别：

```text
.conf     一个顶层 plugin；它内部仍可委托 IPAM
.conflist 多个 plugin 按顺序形成链
```

#### 5.1.2.2 公共字段

| 字段 | 含义 |
|---|---|
| `cniVersion` | 配置和 Result 使用的 CNI 版本 |
| `name` | 网络名称；同一网络的缓存与操作关联标识 |
| `type` | 对应 CNI 插件可执行文件名 |
| `capabilities` | 声明插件可接收哪些 runtime capability args |
| `ipam` | IPAM 委托配置，通常也包含 `type` |
| `dns` | DNS 配置 |
| `plugins` | `.conflist` 中的插件数组 |

插件专属字段由插件定义，不属于 CNI 核心规范。例如 `bridge`、`isGateway`、`hairpinMode` 是 bridge 插件语义；containerd/libcni 只把 JSON 原样传给插件。

#### 5.1.2.3 文件选择与加载

containerd CRI 的默认配置：

```toml
[plugins."io.containerd.cri.v1.runtime".cni]
  conf_dir = "/etc/cni/net.d"
  max_conf_num = 1
```

`max_conf_num` 控制 go-cni 从目录加载多少个顶层网络配置。`1` 是兼容性默认值；`0` 表示不设任意数量上限。

需要区分：

```text
一个 .conflist 中有 3 个 plugin
≠
conf_dir 中加载 3 个顶层网络配置
```

#### 5.1.2.4 配置热更新

containerd 2.2.5 的 CRI 使用 `cniConfSyncer` 监控配置目录：

```text
internal/cri/server/cni_conf_syncer.go
```

其核心机制：

1. 使用 `fsnotify.NewWatcher()`；
2. 监控配置目录及父目录变化；
3. 对 Write/Rename/Remove 等事件重新加载；
4. 目录被替换时重新添加 watcher；
5. 初始化时按权限创建目录。

源码中目录创建权限包括：

```text
父目录：0755
conf dir：0700
```

这意味着很多 CNI 配置更新不需要重启 containerd。但热加载只影响后续操作，已经运行的 Pod 不会自动按新配置重做网络。

#### 5.1.2.5 排序与“哪个配置被选中”

实际加载通常与文件名排序和 `max_conf_num` 有关。因此常见命名：

```text
00-loopback.conf
10-calico.conflist
10-containerd-net.conflist
```

不要仅凭“目录里有配置”判断生效；应结合：

```bash
ls -l /etc/cni/net.d
crictl info
journalctl -u containerd | grep -i cni
```

并确认被加载的网络名称和插件链。

---

### 5.1.3 容器运行时对 CNI 插件的调用

#### 5.1.3.1 CNI 命令

核心命令包括：

```text
ADD      增加或配置网络
CHECK    检查现有网络是否符合预期
DEL      删除网络并回收资源
VERSION  查询插件支持的 CNI 版本
GC/STATUS 新版本规范可提供的扩展生命周期能力
```

在容器创建主路径上最关键的是 ADD，删除主路径上最关键的是 DEL。

#### 5.1.3.2 环境变量

典型变量：

```text
CNI_COMMAND=ADD
CNI_CONTAINERID=<sandbox-id>
CNI_NETNS=/proc/<pid>/ns/net 或持久化 netns 路径
CNI_IFNAME=eth0
CNI_PATH=/opt/cni/bin
CNI_ARGS=K8S_POD_NAME=...;K8S_POD_NAMESPACE=...;...
```

语义：

| 变量 | 作用 |
|---|---|
| `CNI_COMMAND` | 当前操作 |
| `CNI_CONTAINERID` | 运行时分配的唯一 ID |
| `CNI_NETNS` | 目标 network namespace 路径 |
| `CNI_IFNAME` | 目标容器内接口名 |
| `CNI_PATH` | 委托插件搜索路径 |
| `CNI_ARGS` | 运行时传递的额外键值参数 |

Kubernetes 相关信息通常通过 `CNI_ARGS` 或 capability args 传入，但插件不应假设所有 runtime 都有 Kubernetes 字段。

#### 5.1.3.3 stdin JSON 与 stdout Result

调用近似：

```bash
CNI_COMMAND=ADD \
CNI_CONTAINERID=abc \
CNI_NETNS=/var/run/netns/test \
CNI_IFNAME=eth0 \
CNI_PATH=/opt/cni/bin \
/opt/cni/bin/bridge < /etc/cni/net.d/10-test.conf
```

成功时 stdout 是 JSON Result。失败时插件应返回非零退出码和结构化错误或 stderr 诊断。

containerd vendored CNI 库中的关键调用：

```text
vendor/github.com/containernetworking/cni/pkg/invoke/exec.go
  ExecPluginWithResult(...)
```

它会：

1. 执行插件路径；
2. 传入配置与参数；
3. 读取 stdout；
4. 解析 Result；
5. 做版本转换/校验；
6. 把错误包装返回上层。

#### 5.1.3.4 CRI 调用使用 PodSandbox ID

在 Kubernetes 中，网络属于 PodSandbox，而不是每个业务容器都单独调用一次 CNI：

```text
RunPodSandbox
  └─ CNI ADD(sandbox-id, netns)

CreateContainer A
  └─ 加入 sandbox 已有 network namespace

CreateContainer B
  └─ 加入同一个 network namespace
```

这正是同一 Pod 中容器共享 IP 和 localhost 的原因。

hostNetwork Pod 则不需要普通 CNI 网络设置，因为它直接使用宿主 network namespace。

#### 5.1.3.5 capability args

某些数据不适合写死在网络配置中，而由每次运行动态提供，例如：

- `portMappings`；
- `bandwidth`；
- `ips`；
- `mac`；
- `aliases`。

配置通过：

```json
"capabilities": {"portMappings": true}
```

声明插件愿意接收，runtime 再在 RuntimeConf 中传入实际值。只有两边都支持才生效。

---

### 5.1.4 CNI 插件的执行流程

#### 5.1.4.1 单插件 ADD

```text
Runtime
  │
  ├─ 查找 type=bridge → /opt/cni/bin/bridge
  ├─ 组装 CNI 环境变量
  ├─ stdin 发送网络 JSON
  ▼
bridge plugin
  │
  ├─ 解析配置
  ├─ 创建/检查 bridge
  ├─ 创建 veth pair
  ├─ 一端接 bridge
  ├─ 另一端移入目标 netns 并命名 eth0
  ├─ 委托 host-local 分配 IP
  ├─ 配置地址/路由
  └─ stdout 返回 Result
```

具体动作由 bridge 插件版本决定；containerd 只负责调用与处理结果。

#### 5.1.4.2 go-cni Setup

vendored `go-cni` 的核心接口：

```go
Setup(ctx, id, path, opts...)
SetupSerially(ctx, id, path, opts...)
Remove(ctx, id, path, opts...)
Check(ctx, id, path, opts...)
```

`Setup` 对多个已加载网络可以并行设置；`SetupSerially` 串行设置。containerd CRI 通过 `setup_serially` 选择。

在 Linux 默认设置中，loopback 与主网络可以并行，因为 network namespace 创建时 `lo` 已存在，loopback 插件主要负责将其设为 UP。

#### 5.1.4.3 conflist 执行

libcni 的：

```text
AddNetworkList
```

按 `plugins` 顺序调用。后一插件收到前一插件的结果 `prevResult`，从而在已有网络基础上增加功能。

例如：

```text
bridge
  ↓ Result: eth0 + IP + route
portmap
  ↓ prevResult + portMappings
bandwidth
  ↓ prevResult + bandwidth
最终 Result
```

对一个 `.conflist`，当前 vendored libcni 的 `DelNetworkList` 会按 `plugins` 的**逆序**发送 DEL，确保依赖前置网络的 meta plugin 先撤销规则。go-cni 若加载了多个顶层网络，则按其已加载顺序逐个调用各 Network 的 `Remove`；不要把这两个层次混为一谈。

#### 5.1.4.4 缓存

libcni 提供：

```text
GetNetworkListCachedResult
```

缓存可用于：

- CHECK 或 DEL 时恢复 ADD 的结果；
- 插件链之间的结果关联；
- 运行时重启后继续清理。

但缓存不是网络状态的唯一真相。真实状态还在：

- netns；
- netlink 设备；
- IPAM 分配目录/数据库；
- iptables/nftables/eBPF map；
- 路由表。

所以“缓存删除了”不等于网络已清理，“缓存存在”也不保证网卡仍存在。

#### 5.1.4.5 错误回滚

假设链：

```text
bridge ADD 成功
portmap ADD 成功
bandwidth ADD 失败
```

运行时/库需要尽力对已成功插件调用 DEL。现实中回滚也可能失败，于是遗留：

- veth；
- IPAM 地址；
- NAT 规则；
- qdisc；
- CNI cache。

生产排障必须同时检查控制面记录与数据面，而不能只看最后一条 error。

---

### 5.1.5 CNI 插件的委托调用

#### 5.1.5.1 IPAM 委托

bridge 等 main 插件常把 IP 分配委托给 `ipam.type`：

```json
"ipam": {
  "type": "host-local",
  "subnet": "10.88.0.0/16"
}
```

main 插件调用 IPAM 时，会传入同一 CNI command 和经过调整的配置。IPAM 返回 IP、gateway、route，main 插件再将结果应用到容器网卡。

这是一种“插件内部委托”，与 `.conflist` 顶层多插件链是两个层次。

#### 5.1.5.2 `prevResult`

后续插件配置中会注入：

```json
"prevResult": { ... }
```

作用是让 meta 插件知道：

- 哪个 interface 属于容器；
- 分配了哪些 IP；
- sandbox/netns 信息；
- 已有哪些 route/DNS。

插件必须把旧版本 Result 转换为自身支持的版本，再读取。vendored CNI library 负责一部分 Result version conversion。

#### 5.1.5.3 委托的设计价值

通过委托，可以组合：

```text
连接方式：bridge/macvlan/ipvlan
IPAM：host-local/dhcp/static
附加能力：portmap/firewall/bandwidth/tuning
```

避免每个 main 插件都重新实现 IPAM、端口映射和带宽控制。

---

### 5.1.6 CNI 插件接口的输出格式

#### 5.1.6.1 Result 的核心对象

现代 CNI Result 通常包含：

```text
interfaces
ips
routes
dns
```

示意：

```json
{
  "cniVersion": "1.0.0",
  "interfaces": [
    {"name": "cni0", "mac": "..."},
    {"name": "veth1234", "mac": "..."},
    {"name": "eth0", "mac": "...", "sandbox": "/var/run/netns/test"}
  ],
  "ips": [
    {
      "address": "10.88.0.2/16",
      "gateway": "10.88.0.1",
      "interface": 2
    }
  ],
  "routes": [
    {"dst": "0.0.0.0/0", "gw": "10.88.0.1"}
  ],
  "dns": {
    "nameservers": ["10.96.0.10"],
    "search": ["default.svc.cluster.local"]
  }
}
```

`interface: 2` 指向 `interfaces[2]`，把 IP 与容器内 `eth0` 关联。

#### 5.1.6.2 Result 不等于最终所有网络状态

Result 主要表达接口、IP、路由、DNS。很多实现还会在主机侧写入：

- iptables/nftables；
- eBPF program/map；
- policy routing；
- VXLAN/FIB；
- IPAM datastore。

这些不一定完整出现在 Result 中。因此 Result 适合运行时保存关键网络结果，但不能替代插件自身的状态诊断工具。

#### 5.1.6.3 CRI 如何选择 Pod IP

containerd CRI 配置有 `ip_pref`：

```text
"" / ipv4  → 选择第一个 IPv4
ipv6       → 选择第一个 IPv6
cni        → 遵循 CNI Result 原始顺序，取第一个 IP
```

多网卡、双栈和 meta plugin 场景下，IP 顺序会影响 CRI 返回给 kubelet的主 Pod IP。不能只确认 Result 中“有 IP”，还要确认顺序和 family 符合预期。

---

### 5.1.7 手动配置容器网络

#### 5.1.7.1 使用 `ip netns` 理解 CNI 前置条件

```bash
ip netns add demo
ip netns exec demo ip link
ip netns exec demo ip link set lo up
```

此时只有一个孤立 netns。手工 veth：

```bash
ip link add veth-host type veth peer name eth0
ip link set eth0 netns demo
ip addr add 10.88.0.1/24 dev veth-host
ip link set veth-host up
ip netns exec demo ip addr add 10.88.0.2/24 dev eth0
ip netns exec demo ip link set eth0 up
ip netns exec demo ip route add default via 10.88.0.1
```

这组命令展示了 bridge/CNI 背后最基础的内核动作。

清理：

```bash
ip netns del demo
ip link del veth-host 2>/dev/null || true
```

#### 5.1.7.2 直接调用 CNI 插件的危险点

可以直接设置环境变量调用插件，但需要自行保证：

- 唯一 container ID；
- 正确 netns path；
- ADD/DEL 成对；
- IPAM cache 不冲突；
- `CNI_PATH` 正确；
- 配置版本与插件兼容。

建议在独立实验网络和 namespace 中操作，不要拿 Kubernetes 正在使用的 `/etc/cni/net.d` 配置直接造假调用，否则可能污染 IPAM。

#### 5.1.7.3 用 `nsenter` 观察 Pod 网络

```bash
PID=$(crictl inspectp <pod-id> | jq -r '.info.pid // .status.linux.namespaces.options.network')
nsenter -t "$PID" -n ip addr
nsenter -t "$PID" -n ip route
```

不同 crictl/runtime 版本的 inspect 字段可能不同，必要时先查看完整 JSON。也可从 sandbox task PID 获取 netns。

宿主侧同时观察：

```bash
ip -d link
ip route
bridge link
iptables-save
```

这样才能把 CRI Result 与 Linux 实际状态对上。

---

## 5.2 CNI 插件介绍

### 5.2.1 main 类插件

main 类插件通常负责把容器接口接入某种二层/三层网络。

#### 5.2.1.1 bridge

典型模型：

```text
container eth0
    │ veth pair
host veth
    │
   cni0 bridge
    │
host route / NAT / uplink
```

优点：简单、直观、适合单机或作为更复杂方案基础。局限：跨节点网络需要额外 overlay、路由或上层方案。

关键配置常包括：

```text
bridge
isGateway
isDefaultGateway
ipMasq
hairpinMode
mtu
ipam
```

#### 5.2.1.2 macvlan

容器接口表现为挂在宿主物理接口上的独立 MAC：

```text
physical NIC
├── host interface
├── container macvlan A
└── container macvlan B
```

优点是更接近物理二层网络；限制包括交换机 MAC 容量、宿主与 macvlan 子接口默认通信问题、云环境对额外 MAC 的限制。

#### 5.2.1.3 ipvlan

多个容器可共享父接口 MAC，通过不同 IP 区分。适合不希望产生大量 MAC 的场景，但 L2/L3 模式、宿主通信与路由规划更复杂。

#### 5.2.1.4 ptp

创建 veth point-to-point 连接，宿主侧为每个容器配置路由，而不依赖 Linux bridge。模型简单，但大规模路由和跨节点仍需上层管理。

#### 5.2.1.5 loopback

负责把目标 netns 中已有 `lo` 设置为 UP。它看起来简单，却是 Pod 内 `localhost` 正常工作的基础。

containerd 2.2.5 还支持 `use_internal_loopback`，可不调用 loopback 二进制，由内部机制处理。默认仍使用 CNI loopback 配置。

#### 5.2.1.6 第三方主网络插件

Calico、Cilium、Flannel 等通常不仅是一个简单二进制，还包含 daemon、controller、agent、数据存储和数据面。CNI executable 只是把 Pod 创建/删除事件接入整个网络系统的入口。

因此排障时要分层：

```text
CNI command 成功否
node agent 正常否
路由/隧道/eBPF 正常否
网络策略正常否
跨节点底层网络正常否
```

---

### 5.2.2 IPAM 类插件

#### 5.2.2.1 host-local

本机维护地址分配状态，按 subnet/range 分配 IP。优点是简单，无需中心服务；缺点是多节点全局地址协调需要上层确保每个节点网段不重叠。

典型：

```json
"ipam": {
  "type": "host-local",
  "ranges": [[
    {"subnet": "10.88.0.0/16", "rangeStart": "10.88.0.10"}
  ]],
  "routes": [{"dst": "0.0.0.0/0"}]
}
```

IPAM 状态目录遗留时，可能出现“地址明明没人用却分配失败”。清理必须确认对应容器确已不存在，不能盲删整个 IPAM 数据目录。

#### 5.2.2.2 dhcp

通过 DHCP 获取地址。通常需要宿主上的 DHCP helper/daemon 协助维持 lease。适合接入已有企业二层网络，但依赖外部 DHCP 可达性和租约生命周期。

#### 5.2.2.3 static

由配置或 capability args 直接指定地址。适合实验、固定设施或由上层系统精确分配的场景，不适合人工管理大量动态 Pod。

#### 5.2.2.4 集中式 IPAM

一些网络方案使用 Kubernetes CRD、etcd 或控制器做全局 IPAM。此时 CNI 调用只是事务入口，地址状态在外部 datastore。排障需要查插件自身 IPAM 状态，而非 host-local 目录。

---

### 5.2.3 meta 类插件

meta 插件通常不负责创建主连接，而是基于 `prevResult` 增加能力。

#### 5.2.3.1 portmap

把 runtime 传入的端口映射转换为主机 NAT/转发规则。它常与 nerdctl 的 `-p` 场景相关；Kubernetes Service/NodePort 并不等同于 CNI portmap。

#### 5.2.3.2 bandwidth

根据 capability args 设置 ingress/egress 带宽限制，底层可能使用 qdisc/ifb。它解决的是 Pod 接口限速，不是 Kubernetes requests/limits 中 CPU、内存资源限制。

#### 5.2.3.3 tuning

调整 sysctl、MAC、MTU 等接口属性。错误 MTU 在 overlay 网络中会导致大包、TLS、webhook 等“小包通、大包不通”问题，因此 tuning 类配置必须与底层隧道开销一致。

#### 5.2.3.4 firewall

增加允许/隔离规则。不要把它与 Kubernetes NetworkPolicy 控制器完全等同；具体能力取决于插件实现和调用链位置。

#### 5.2.3.5 sbr

基于源地址配置策略路由，适合多网卡、多 IP 容器避免回程从错误接口出去。

#### 5.2.3.6 multus 类 meta plugin

多网络 meta plugin 可以把一个 Pod 接入多个 CNI 网络：

```text
eth0 → 集群默认网络
net1 → 存储网络
net2 → 高性能 SR-IOV 网络
```

containerd CRI 仍然只调用默认 CNI 配置；由 meta plugin 根据 annotation 再委托其他网络。此时最终 Result 和 Pod IP 选择需特别关注。

---

## 5.3 containerd 中 CNI 的使用

### 5.3.1 containerd 中 CNI 的安装与部署

#### 5.3.1.1 containerd 二进制包不等于 CNI 插件包

检查：

```bash
ls -l /opt/cni/bin
ls -l /etc/cni/net.d
```

至少要同时具备：

```text
CNI executable
CNI network config
```

只有二进制无配置：CRI NetworkReady 通常失败。只有配置无二进制：调用时找不到 `type` 对应 executable。

#### 5.3.1.2 CRI 配置

```toml
[plugins."io.containerd.cri.v1.runtime".cni]
  bin_dirs = ["/opt/cni/bin"]
  conf_dir = "/etc/cni/net.d"
  max_conf_num = 1
  setup_serially = false
```

修改后验证：

```bash
containerd config dump | sed -n '/io.containerd.cri.v1.runtime/,/io.containerd.grpc.v1.cri/p'
crictl info
```

#### 5.3.1.3 版本兼容

本源码 `go.mod` 中记录：

```text
github.com/containernetworking/cni v1.3.0
github.com/containernetworking/plugins v1.9.0
github.com/containerd/go-cni v1.1.13
```

这是 containerd 2.2.5 构建时依赖基线，不应理解成宿主必须安装完全相同版本的 CNI plugin binaries。真正兼容取决于：

- 配置 `cniVersion`；
- 插件通过 VERSION 宣告的支持版本；
- runtime 使用的命令和 capability；
- 插件自身发行说明。

#### 5.3.1.4 CNI Ready 排障清单

```bash
crictl info
journalctl -u containerd | grep -iE 'cni|network'
ls -la /etc/cni/net.d
ls -la /opt/cni/bin
```

逐项确认：

1. 配置 JSON 是否有效；
2. `type` 对应二进制是否存在且可执行；
3. conf_dir 权限；
4. bin_dirs 是否写对；
5. 主机内核模块和 sysctl；
6. 插件 node agent 是否 ready；
7. CNI 配置是否被错误文件排序抢占；
8. MTU、路由和防火墙是否满足数据面要求。

---

### 5.3.2 nerdctl 使用 CNI

#### 5.3.2.1 边界说明

nerdctl 是独立项目，不在 containerd 2.2.5 源码树中。它通过 containerd Native API 管理容器，同时为用户提供类似 Docker CLI 的网络体验，并调用 CNI 管理独立容器网络。

典型分层：

```text
nerdctl
  ├─ containerd client → image/container/task/snapshot
  ├─ CNI              → network namespace
  ├─ BuildKit         → image build
  └─ compose support  → multi-container UX
```

#### 5.3.2.2 为什么 nerdctl `run` 能联网，而 ctr `run` 默认不行

因为 nerdctl 在 containerd Native API 之外补上了网络编排：

1. 创建容器；
2. 创建 task/netns；
3. 调用 CNI ADD；
4. 保存网络 metadata；
5. 删除时调用 CNI DEL。

而 ctr 的定位是低层调试，不承担完整 Docker-style UX。

#### 5.3.2.3 namespace 与网络状态

nerdctl 可以使用 containerd namespace。不同 namespace 的容器 metadata 被隔离，但宿主 CNI bridge、iptables 或 IPAM 可能仍是共享数据面。不要把 containerd namespace 当成网络租户隔离边界。

#### 5.3.2.4 rootless 网络

rootless 模式无法直接执行所有宿主级 netlink、bridge 和 iptables 操作，常需要用户态网络或 rootlesskit/slirp 等机制。它与 rootful CNI 的数据路径、性能和端口转发方式不同。

---

### 5.3.3 CRI 使用 CNI

#### 5.3.3.1 初始化 CNI

Linux 初始化大致为：

```text
CRI Runtime plugin config
  ↓
service_linux.go
  ↓
cni.New(
  WithPluginConfDir,
  WithPluginMaxConfNum,
  WithPluginDir,
  WithMinNetworkCount,
)
  ↓
Load / Sync config
```

CNI 对象按 runtime class 的配置目录也可以不同。Runtime struct 中：

```text
cni_conf_dir
cni_max_conf_num
```

允许某个 RuntimeHandler 覆盖全局 CNI 配置。

#### 5.3.3.2 RunPodSandbox 网络时序

```text
创建 lease、sandbox store metadata
  ↓
（非 hostNetwork）创建 sandbox netns 并保存 path
  ↓
构造 NamespaceOpts
  ├─ Pod name/namespace/UID
  ├─ labels/annotations
  ├─ port mappings
  ├─ bandwidth
  └─ DNS/其他 capability args
  ↓
Setup 或 SetupSerially
  ↓
保存 Result / Pod IP
  ↓
创建并启动 sandbox controller/task
  ↓
返回 RunPodSandboxResponse
```

业务容器随后通过 OCI namespace path 加入该网络命名空间。

#### 5.3.3.3 删除网络时序

```text
StopPodSandbox
  ├─ 强制停止业务容器和 sandbox
  ├─ CNI DEL
  ├─ 删除 network namespace
  └─ 清理 image mounts

RemovePodSandbox
  ├─ 再次调用 stop（因此对尚未停止的 sandbox 仍会做上述清理）
  ├─ 删除 lease、CRI containers、sandbox controller
  └─ 删除 sandbox metadata/store
```

实际代码会考虑失败恢复和顺序。若 netns 已关闭，2.2.5 会把传给 CNI DEL 的 netns path 置空；libcni 对此做 best-effort 处理，但插件实现并不完全一致，因此仍可能留下 IPAM 或主机侧资源。

#### 5.3.3.4 网络配置变化不会自动改现有 Pod

CNI 配置热更新只用于后续 ADD。要让已有 Pod 使用新 MTU、网段或插件链，通常需要重建 Pod。直接在宿主手改 veth 可能让控制面状态与数据面漂移。

---

### 5.3.4 ctr 使用 CNI

#### 5.3.4.1 默认不配网，但有受限的 `--cni` 调试开关

不带选项时，`ctr run` 不调用 CNI。containerd 2.2.5 的 `ctr run` 另有 `--cni`：它用 go-cni 的 `WithDefaultConf` 加载默认 CNI 配置，创建 Task 后从 Task PID 取得 netns，在 `Start` 前执行 `Setup`。

最小实验：

```bash
ctr run --rm --cni docker.io/library/busybox:latest cni-demo ip addr
```

但这不是 CRI/nerdctl 那样完整的网络编排：它没有 Kubernetes 的 capability args、Pod sandbox、端口映射/DNS 编排和持久网络 metadata；更重要的是，源码只在**非 detached** 路径的 defer 中调用 `Remove`。因此不要把 `ctr run -d --cni` 当成有可靠 DEL/崩溃恢复保证的生产方案。

这仍符合工具定位：

```text
ctr = containerd 原生能力调试器
```

#### 5.3.4.2 使用 host network

实验中最简单：

```bash
ctr run --rm --net-host docker.io/library/busybox:latest demo ip addr
```

这会让容器使用宿主 network namespace，不涉及 CNI。它不具备网络隔离，不适合作为通用生产方案。

#### 5.3.4.3 手工 CNI 思路

当需要指定非默认 CNI 配置、观察调用参数，或验证 `--cni` 未覆盖的场景时，可以手工配置；前提是网络 namespace 在 task 运行期间存在：

```text
ctr 创建 container/task
  ↓
取得 task PID
  ↓
netns path = /proc/<pid>/ns/net
  ↓
调用 CNI ADD(container-id, netns path, eth0)
  ↓
容器中验证网络
  ↓
停止前/删除时调用 CNI DEL
```

问题在于：若容器进程立即退出，`/proc/<pid>/ns/net` 会消失。因此常用一个长运行命令：

```bash
ctr run -d docker.io/library/busybox:latest netdemo sleep 3600
PID=$(ctr tasks list | awk '$1=="netdemo" {print $2}')
readlink /proc/$PID/ns/net
```

随后使用 `cnitool` 或自行调用 libcni/CNI binary。`cnitool` 也是外部工具，不在 containerd 源码中。

#### 5.3.4.4 为什么不建议用 ctr 手工网络跑生产

你需要自行实现：

- ADD/DEL 配对；
- container ID 与网络 metadata 持久化；
- daemon/进程崩溃恢复；
- 多插件回滚；
- port mapping；
- DNS 文件；
- 并发 IPAM；
- 网络清理与 GC。

这些正是 nerdctl 或 CRI 已经承担的工作。ctr 手工 CNI 更适合学习与定位问题。

---

## 5.4 源码调用链精读

### 5.4.1 配置到 CNI 对象

```text
internal/cri/config/config.go
  CniConfig
    │
    ▼
plugins/cri/runtime/plugin.go
  DefaultRuntimeConfig / ValidateRuntimeConfig
    │
    ▼
internal/cri/server/service_linux.go
  cni.New + options
    │
    ▼
vendor/github.com/containerd/go-cni
  load network configs
```

### 5.4.2 Setup 到插件进程

```text
internal/cri/server/sandbox_run.go
  netPlugin.Setup / SetupSerially
    │
    ▼
vendor/github.com/containerd/go-cni/cni.go
  namespace.attach / setup
    │
    ▼
vendor/github.com/containerd/go-cni/namespace.go
  AddNetworkList
    │
    ▼
vendor/github.com/containernetworking/cni/libcni/api.go
  AddNetworkList / addNetwork
    │
    ▼
vendor/github.com/containernetworking/cni/pkg/invoke/exec.go
  ExecPluginWithResult
    │
    ▼
/opt/cni/bin/<type>
```

### 5.4.3 配置监听

```text
internal/cri/server/cni_conf_syncer.go
  fsnotify
    │
    ├─ Write/Rename/Remove
    └─ reload go-cni config
```

阅读时重点关注锁、事件合并和目录被替换的处理，因为 Kubernetes 常通过原子 rename 更新 ConfigMap/文件。

---

## 5.5 常见故障的因果链

### 5.5.1 `NetworkPluginNotReady: cni config uninitialized`

```text
conf_dir 没文件
或配置无法解析
或 max_conf_num/排序选错
或插件初始化失败
  ↓
CNI 未达到 min network count
  ↓
CRI Status NetworkReady=false
  ↓
kubelet 无法创建 PodSandbox
```

### 5.5.2 `failed to find plugin "bridge" in path`

```text
配置 type=bridge
  ↓
bin_dirs 中不存在可执行 bridge
  ↓
libcni FindInPath 失败
```

检查架构、执行权限和目录，而不是只改配置文件。

### 5.5.3 Pod 有 IP 但跨节点不通

CNI ADD 成功只说明本节点配置阶段成功。跨节点还依赖：

```text
节点路由 / BGP / VXLAN / IPIP / Geneve
底层 MTU
防火墙
rp_filter
云安全组
网络策略
```

因此不能把所有网络问题都归结为“containerd 调 CNI 失败”。

### 5.5.4 小包通、大包不通

典型因果链：

```text
Pod MTU 过大
  + overlay/IPIP/VXLAN 封装开销
  + 路径设备 MTU 更小
  + ICMP fragmentation-needed 被丢弃
  ↓
PMTU 学习失败
  ↓
TCP 小请求正常，大请求/HTTPS/webhook 超时
```

CNI tuning 或主网络插件通常负责设置 Pod veth MTU，必须根据实际底层路径计算，而不能只看宿主 `eth0=1500`。

### 5.5.5 删除 Pod 后 IP 不释放

可能链路：

```text
RemovePodSandbox 未执行
CNI DEL 失败
netns 已消失且插件处理不完善
IPAM datastore 不可用
containerd/store 状态残留
```

需要对照：

- `crictl pods -a`；
- containerd 日志；
- CNI cache/IPAM；
- 插件 controller/agent 日志；
- 实际 veth 和路由。

---

## 5.6 与 containerd 1.7.1 参考书对照

| 原书理解 | 2.2.5 需要补充 |
|---|---|
| CRI CNI 配置在一个大 CRI 插件块 | 配置位于 `io.containerd.cri.v1.runtime.cni` |
| `bin_dir` | 已废弃，优先 `bin_dirs` |
| containerd 自带容器网络 | containerd 调用外部 CNI，可执行插件独立安装 |
| CNI 只是一张 JSON | 还有环境变量、stdin/stdout、版本协商、缓存、回滚 |
| 每个容器调用 CNI | Kubernetes 主要为 PodSandbox 调 CNI，业务容器共享 netns |
| ctr、nerdctl、CRI 网络行为相同 | ctr 默认不编排 CNI；nerdctl 与 CRI 各自承担网络生命周期 |
| 改 CNI 配置需重启 | 2.2.5 CRI 有 fsnotify 配置同步，但只影响新操作 |

---

## 5.7 本章实验

### 实验一：验证 CNI 文件与插件匹配

```bash
for f in /etc/cni/net.d/*; do
  echo "=== $f ==="
  grep -o '"type"[[:space:]]*:[[:space:]]*"[^"]*"' "$f" || true
done

ls -l /opt/cni/bin
```

逐个确认 `type` 对应 executable。

### 实验二：追踪 sandbox 网络命名空间

```bash
crictl pods
crictl inspectp <pod-id> > /tmp/pod.json
```

找到 PID 后：

```bash
nsenter -t <pid> -n ip -d addr
nsenter -t <pid> -n ip route
```

再在宿主查看 veth peer：

```bash
ip -d link
```

### 实验三：观察配置热加载

在实验节点：

```bash
journalctl -u containerd -f
```

对 CNI 配置做原子替换：

```bash
cp 10-test.conflist 10-test.conflist.new
mv 10-test.conflist.new /etc/cni/net.d/10-test.conflist
```

观察 reload 日志。不要在承载生产 Pod 的节点随意改网段或插件类型。

### 实验四：用 ctr 体验“没有网络编排”

```bash
ctr image pull docker.io/library/busybox:latest
ctr run --rm docker.io/library/busybox:latest no-net ip addr
ctr run --rm --net-host docker.io/library/busybox:latest host-net ip addr
```

比较 network namespace 与接口差异。

---

## 5.8 本章结论

1. CNI 是运行时调用外部网络插件的协议，不是具体网络实现。
2. containerd 2.2.5 的 CRI 通过 go-cni/libcni 调用插件，并监控 CNI 配置目录变化。
3. Kubernetes 网络配置发生在 PodSandbox 层，业务容器加入 sandbox 的 netns。
4. `.conflist` 通过 `prevResult` 把 main、IPAM、meta 插件组合起来。
5. ADD 成功不代表跨节点数据面一定正常；路由、隧道、MTU、策略仍需独立验证。
6. nerdctl 会补充 Native API 之外的 CNI 编排，ctr 默认不会。
7. 从 1.7 阅读迁移到 2.2.5 时，应重点关注 CRI 配置新 URI、`bin_dirs`、配置热加载和 runtime-specific CNI。
