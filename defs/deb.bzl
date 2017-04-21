load("@bazel_tools//tools/build_defs/pkg:pkg.bzl", "pkg_tar", "pkg_deb")

KUBERNETES_AUTHORS = "Kubernetes Authors <kubernetes-dev+release@googlegroups.com>"

KUBERNETES_HOMEPAGE = "http://kubernetes.io"

def k8s_deb(name, depends = [], description = ""):
  pkg_deb(
      name = name,
      architecture = "amd64",
      data = name + "-data",
      depends = depends,
      description = description,
      homepage = KUBERNETES_HOMEPAGE,
      maintainer = KUBERNETES_AUTHORS,
      package =  name,
      version = "1.6.0-alpha",
  )

def deb_data(name, data = []):
  deps = []
  for i, info in enumerate(data):
    dname = "%s-deb-data-%s" % (name, i)
    deps += [dname]
    pkg_tar(
        name = dname,
        files = info["files"],
        mode = info["mode"],
        package_dir = info["dir"],
    )
  pkg_tar(
      name = name + "-data",
      deps = deps,
  )
