---
name: Bug report
about: Create a bug report to help improve containerd
title: ''
labels: kind/bug
assignees: ''
---

<!--
If you are reporting a new issue, make sure that we do not have any duplicates
already open. You can ensure this by searching the issue list for this
repository. If there is a duplicate, please close your issue and add a comment
to the existing issue instead.
-->

**Description**

<!--
Briefly describe the problem you are having in a few paragraphs.
-->

**Steps to reproduce the issue:**
1.
2.
3.

**Describe the results you received:**


**Describe the results you expected:**


**What version of containerd are you using:**

```
$ containerd --version

```

**Any other relevant information (runC version, CRI configuration, OS/Kernel version, etc.):**

<!--
Tips:

* If containerd gets stuck on something and enables debug socket, `ctr pprof goroutines`
  dumps the golang stack of containerd, which is helpful! If containerd runs
  without debug socket, `kill -SIGUSR1 $(pidof containerd)` also dumps the stack
  as well.

* If there is something about running containerd, like consuming more CPU resources,
  `ctr pprof` subcommands will help you to get some useful profiles. Enable debug
  socket makes life easier.
-->

<details><summary><code>runc --version</code></summary><br><pre>
$ runc --version

</pre></details>

<!--
Show related configuration if it is related to CRI plugin.
-->

<details><summary><code>crictl info</code></summary><br><pre>
$ crictl info

</pre></details>


<details><summary><code>uname -a</code></summary><br><pre>
$ uname -a

</pre></details>
