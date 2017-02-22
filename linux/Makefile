
all:
	go build -buildmode=plugin -o shim-linux-amd64.so

install:
	mkdir -p /var/lib/containerd/plugins
	cp shim-linux-amd64.so /var/lib/containerd/plugins/
