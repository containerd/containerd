# containerd 2.2.5 源码伴读：第 2～8 章

本目录按《containerd 原理剖析与实战》的章节结构，基于用户提供的 `containerd-2.2.5` 源码包编写。

## 文档目录

| 章节 | 文档 | 重点 |
|---|---|---|
| 第 2 章 | [伴读](containerd-2.2.5-chapter2-companion.md) | Linux 隔离原语、OCI、Image/Container/Task、Runtime v2 的前置模型 |
| 第 3 章 | [伴读](chapter3-using-containerd-2.2.5.md) | 安装、systemd、配置版本 3、ctr、nerdctl 边界 |
| 第 4 章 | [伴读](chapter4-containerd-cloud-native-2.2.5.md) | Kubernetes、CRI、2.2.5 CRI 三插件拆分、crictl |
| 第 5 章 | [伴读](chapter5-container-networking-2.2.5.md) | CNI 配置/调用链、go-cni/libcni、CRI/nerdctl/ctr 网络差异 |
| 第 6 章 | [伴读](chapter6-container-storage-2.2.5.md) | Content、Image、Snapshot、GC、native/overlayfs/devmapper |
| 第 7 章 | [伴读](chapter7-core-components-2.2.5.md) | 插件依赖图、API/Core/Backend、Runtime v2、shim、NRI |
| 第 8 章 | [伴读](chapter8-production-practice-2.2.5.md) | Metrics、Prometheus/Grafana、Go Client、NRI 插件开发 |

## 源码基线

containerd 2.2.5 源码中的关键依赖：

```text
Go module:              github.com/containerd/containerd/v2
Go toolchain requirement: 1.25.0
Config version:         3
containerd API:         v1.10.0
OCI image-spec:         v1.1.1
OCI runtime-spec:       v1.3.0
CNI spec/library:       v1.3.0
containerd/go-cni:      v1.1.13
CNI plugins dependency: v1.9.0
CRI API:                v0.34.1
NRI:                    v0.11.0
runc test baseline:     v1.3.6
crictl test baseline:   v1.33.0
```

这些是源码包记录的构建或测试依赖，不代表所有外部二进制必须机械使用完全相同版本。

## 文档原则

每章尽量采用：

```text
原书概念
  ↓
containerd 2.2.5 源码位置
  ↓
关键对象与调用链
  ↓
为什么这样设计
  ↓
常见故障的因果链
  ↓
可验证实验
  ↓
与 1.7.1 的差异
```

对于 nerdctl、crictl、Prometheus、Grafana、CNI plugin binaries 等独立项目，文档明确区分 containerd 源码事实与外部生态边界，避免把外部实现误写成 containerd 内置代码。

## 推荐阅读顺序

```text
第 2 章：先建立 Linux、OCI、Image/Container/Task 与 Runtime v2 的底层模型
  ↓
第 3 章：再学习安装、配置和对象操作
  ↓
第 7 章：建立 daemon、插件、API、shim 的整体地图
  ↓
第 4 章：理解 Kubernetes/CRI 如何进入这张地图
  ↓
第 5 章：理解 PodSandbox 的网络配置
  ↓
第 6 章：理解镜像与 rootfs 存储
  ↓
第 8 章：监控、编程和扩展实践
```

若目标是处理 Kubernetes 节点故障，可在读完第 2、3、7 章后优先读第 4、5、6、8 章；这样每个排障命令都能对应到 API、插件和底层对象，而不是只记结论。
