# Kubernetes Cluster with Containerd

This document provides the steps to bring up a Kubernetes cluster using ansible and kubeadm tools.

### Prerequisites:
- **OS**: Ubuntu 16.04 (will be updated with additional distros after testing)
- **Python**: 2.7+
- **Ansible**: 2.4+

## Step 0:
-  Install Ansible on the host where you will provision the cluster. This host may be one of the nodes you plan to include in your cluster. Installation instructions for Ansible are found [here](http://docs.ansible.com/ansible/latest/intro_installation.html).
-  Create a hosts file and include the IP addresses of the hosts that need to be provisioned by Ansible.
```console
$ cat hosts
172.31.7.230
172.31.13.159
172.31.1.227
```
-  Setup passwordless SSH access from the host where you are running Ansible to all the hosts in the hosts file. The instructions can be found in [here](http://www.linuxproblem.org/art_9.html)

## Step 1:
At this point, the ansible playbook should be able to ssh into the machines in the hosts file.
```console
git clone https://github.com/containerd/containerd
cd ./containerd/contrib/ansible
ansible-playbook -i hosts cri-containerd.yaml
```
A typical cloud login might have a username and private key file, in which case the following can be used:
```console
ansible-playbook -i hosts -u <username> --private-key <example.pem> cri-containerd.yaml
 ```
For more options ansible config file (/etc/ansible/ansible.cfg) can be used to set defaults. Please refer to [Ansible options](http://docs.ansible.com/ansible/latest/intro_configuration.html) for advanced ansible configurations.

At the end of this step, you will have the required software installed in the hosts to bringup a kubernetes cluster.
```console
PLAY RECAP ***************************************************************************************************************************************************************
172.31.1.227               : ok=21   changed=7    unreachable=0    failed=0
172.31.13.159              : ok=21   changed=7    unreachable=0    failed=0
172.31.7.230               : ok=21   changed=7    unreachable=0    failed=0
```

## Step 2:
Use [kubeadm](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/) to bring up a Kubernetes Cluster. Depending on what third-party provider you choose, you might have to set the ```--pod-network-cidr``` to something provider-specific.
Initialize the cluster from one of the nodes (Note: This node will be the master node):
```console
$sudo kubeadm init --skip-preflight-checks
[kubeadm] WARNING: kubeadm is in beta, please do not use it for production clusters.
[init] Using Kubernetes version: v1.7.6
[init] Using Authorization modes: [Node RBAC]
[preflight] Skipping pre-flight checks
[kubeadm] WARNING: starting in 1.8, tokens expire after 24 hours by default (if you require a non-expiring token use --token-ttl 0)
[certificates] Generated CA certificate and key.
[certificates] Generated API server certificate and key.
[certificates] API Server serving cert is signed for DNS names [abhi-k8-ubuntu-1 kubernetes kubernetes.default kubernetes.default.svc kubernetes.default.svc.cluster.local] and IPs [10.96.0.1 172.31.7.230]
[certificates] Generated API server kubelet client certificate and key.
[certificates] Generated service account token signing key and public key.
[certificates] Generated front-proxy CA certificate and key.
[certificates] Generated front-proxy client certificate and key.
[certificates] Valid certificates and keys now exist in "/etc/kubernetes/pki"
[kubeconfig] Wrote KubeConfig file to disk: "/etc/kubernetes/admin.conf"
[kubeconfig] Wrote KubeConfig file to disk: "/etc/kubernetes/kubelet.conf"
[kubeconfig] Wrote KubeConfig file to disk: "/etc/kubernetes/controller-manager.conf"
[kubeconfig] Wrote KubeConfig file to disk: "/etc/kubernetes/scheduler.conf"
[apiclient] Created API client, waiting for the control plane to become ready
[apiclient] All control plane components are healthy after 42.002391 seconds
[token] Using token: 43a25d.420ff2e06336e4c1
[apiconfig] Created RBAC rules
[addons] Applied essential addon: kube-proxy
[addons] Applied essential addon: kube-dns

Your Kubernetes master has initialized successfully!

To start using your cluster, you need to run (as a regular user):

  mkdir -p $HOME/.kube
  sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
  sudo chown $(id -u):$(id -g) $HOME/.kube/config

You should now deploy a pod network to the cluster.
Run "kubectl apply -f [podnetwork].yaml" with one of the options listed at:
  https://kubernetes.io/docs/concepts/cluster-administration/addons/

You can now join any number of machines by running the following on each node
as root:

  kubeadm join --token 43a25d.420ff2e06336e4c1 172.31.7.230:6443

```
## Step 3:
Use kubeadm join to add each of the remaining nodes to your cluster. (Note: Uses token that was generated during cluster init.)
```console
$sudo kubeadm join --token 43a25d.420ff2e06336e4c1 172.31.7.230:6443 --skip-preflight-checks
[kubeadm] WARNING: kubeadm is in beta, please do not use it for production clusters.
[preflight] Skipping pre-flight checks
[discovery] Trying to connect to API Server "172.31.7.230:6443"
[discovery] Created cluster-info discovery client, requesting info from "https://172.31.7.230:6443"
[discovery] Cluster info signature and contents are valid, will use API Server "https://172.31.7.230:6443"
[discovery] Successfully established connection with API Server "172.31.7.230:6443"
[bootstrap] Detected server version: v1.7.6
[bootstrap] The server supports the Certificates API (certificates.k8s.io/v1beta1)
[csr] Created API client to obtain unique certificate for this node, generating keys and certificate signing request
[csr] Received signed certificate from the API server, generating KubeConfig...
[kubeconfig] Wrote KubeConfig file to disk: "/etc/kubernetes/kubelet.conf"

Node join complete:
* Certificate signing request sent to master and response
  received.
* Kubelet informed of new secure connection details.

Run 'kubectl get nodes' on the master to see this machine join.
```
At the end of Step 3 you should have a kubernetes cluster up and running and ready for deployment.

## Step 4:
Please follow the instructions [here](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/create-cluster-kubeadm/#pod-network) to deploy CNI network plugins and start a demo app.

We are constantly striving to improve the installer. Please feel free to open issues and provide suggestions to make the installer fast and easy to use. We are open to receiving help in validating and improving the installer on different distros.
