## cni-bridge-f(ail)p(oint)

### Overview

The `cni-bridge-fp` is a CNI plugin which delegates interface-creating function
to [CNI bridge plugin][1] and allows user to inject failpoint before delegation.

Since the CNI plugin is invoked by binary call from CRI and it is short-lived,
the failpoint need to be configured by a JSON file, which can be persisted.
There is an example about failpoint description.

```json
{
	"cmdAdd":   "1*error(you-shall-not-pass!)->1*panic(again)",
	"cmdDel":   "1*error(try-again)",
	"cmdCheck": "10*off"
}
```

* `cmdAdd` (string, optional): The failpoint for `ADD` command.
* `cmdDel` (string, optional): The failpoint for `DEL` command.
* `cmdCheck` (string, optional): The failpoint for `CHECK` command.

Since the `cmdXXX` can be multiple failpoints, each CNI binary call will update
the current state to make sure the order of execution is expected.

And the failpoint injection is enabled by pod's annotation. Currently, the key
of customized CNI capabilities in containerd can only be `io.kubernetes.cri.pod-annotations`
and containerd will pass pod's annotations to CNI under the that object. The
user can use the `failpoint.cni.containerd.io/confpath` annotation to enable
failpoint for the pod.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  annotations:
    failpoint.cni.containerd.io/confpath: "/tmp/pod-failpoints.json"
spec:
  containers:
  - name: nginx
    image: nginx:1.14.2
    ports:
    - containerPort: 80
```

### Example

Let's use the following json as failpoint description.

```bash
$ cat <<EOF | tee /tmp/cni-failpoint.json
{
	"cmdAdd":   "1*error(try-again)",
	"cmdDel":   "2*error(oops)",
	"cmdCheck": "1*off->1*panic(sorry)"
}
EOF
```

And use `ip netns` to create persisted net namespace named by `failpoint`.

```bash
$ sudo ip netns add failpoint
```

And then setup the following bash script for demo.

```bash
$ cat <<EOFDEMO | tee /tmp/cni-failpoint-demo-helper.sh
#!/usr/bin/env bash

export CNI_CONTAINERID=failpoint-testing
export CNI_NETNS=/run/netns/failpoint
export CNI_IFNAME=fpeni0
export CNI_PATH=/opt/cni/bin/

cat <<EOF | /opt/cni/bin/cni-bridge-fp
{
  "cniVersion": "0.3.0",
  "name": "containerd-net-fp",
  "type": "cni-bridge-fp",
  "bridge": "fp-cni0",
  "isGateway": true,
  "ipMasq": true,
  "promiscMode": true,
  "ipam": {
    "type": "host-local",
    "ranges": [
      [{
        "subnet": "10.88.0.0/16"
      }],
      [{
        "subnet": "2001:4860:4860::/64"
      }]
    ],
    "routes": [
      { "dst": "0.0.0.0/0" },
      { "dst": "::/0" }
    ]
  },
  "runtimeConfig": {
    "io.kubernetes.cri.pod-annotations": {
      "failpoint.cni.containerd.io/confpath": "/tmp/cni-failpoint.json"
    }
  }
}
EOF

EOFDEMO
```

Let's try to setup CNI and we should get a error `try-again`.

```bash
$ sudo CNI_COMMAND=ADD bash /tmp/cni-failpoint-demo-helper.sh
{
    "code": 999,
    "msg": "try-again"
}

# there is no failpoint for ADD command.
$ cat /tmp/cni-failpoint.json | jq .
{
  "cmdAdd": "0*error(try-again)",
  "cmdDel": "2*error(oops)",
  "cmdCheck": "1*off->1*panic(sorry)"
}
```

We should setup CNI successfully after retry. When we teardown the interface,
there should be two failpoints.

```bash
$ sudo CNI_COMMAND=ADD bash /tmp/cni-failpoint-demo-helper.sh
...

$ sudo CNI_COMMAND=DEL bash /tmp/cni-failpoint-demo-helper.sh
{
    "code": 999,
    "msg": "oops"
}

$ sudo CNI_COMMAND=DEL bash /tmp/cni-failpoint-demo-helper.sh
{
    "code": 999,
    "msg": "oops"
}

$ cat /tmp/cni-failpoint.json | jq .
{
  "cmdAdd": "0*error(try-again)",
  "cmdDel": "0*error(oops)",
  "cmdCheck": "1*off->1*panic(sorry)"
}
```

[1]: <https://www.cni.dev/plugins/current/main/bridge/>
