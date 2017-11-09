parallel(
    "strea1": {
        node ("amd64") {
            checkout scm
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make check'
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make binaries'
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make checkprotos'
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make test'
        }
    },
    "stream2": {
        node ("aarch64") {
            checkout scm
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make check'
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make binaries'
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make checkprotos'
            sh 'docker run -v $PWD:/go/src/github.com/containerd/containerd -w /go/src/github.com/containerd/containerd --rm gianarb/containerd:build make test'
        }
    }
)
