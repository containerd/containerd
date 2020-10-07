# Release Process
This document describes how to cut a `cri` plugin release.

## Step 1: Update containerd vendor
Update the version of containerd located in `containerd/cri/vendor.conf`
to the latest version of containerd for the desired branch of containerd,
and make sure all tests in CI pass https://k8s-testgrid.appspot.com/sig-node-containerd.
## Step 2: Cut the release
Draft and tag a new release in https://github.com/containerd/cri/releases.
## Step 3: Update `cri` version in containerd
Push a PR to `containerd/containerd` that updates the version of
`containerd/cri` in `containerd/containerd/vendor.conf` to the newly
tagged release created in Step 2.
## Step 4: Iterate step 1 updating containerd vendor
## Step 5: Publish release tarball for Kubernetes
Publish the release tarball `cri-containerd-${CONTAINERD_VERSION}.${OS}-${ARCH}.tar.gz`
```shell
# Checkout `containerd/cri` to the newly released version.
git checkout ${RELEASE_VERSION}

# Publish the release tarball without cni.
DEPLOY_BUCKET=cri-containerd-release make push TARBALL_PREFIX=cri-containerd OFFICIAL_RELEASE=true VERSION=${CONTAINERD_VERSION}

# Publish the release tarball with cni.
DEPLOY_BUCKET=cri-containerd-release make push TARBALL_PREFIX=cri-containerd-cni OFFICIAL_RELEASE=true INCLUDE_CNI=true VERSION=${CONTAINERD_VERSION}
```
## Step 6: Update release note with release tarball information
