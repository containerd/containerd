## Requirements

### Running
Due to its dependency on `dmsetup`, executing the snapshotter process in an environment where a udev
daemon is not accessible (such as a container) may result in unexpected behavior. In this case, try executing the
snapshotter with the `DM_DISABLE_UDEV=1` environment variable, which tells `dmsetup` to ignore udev and manage devices
itself. See [lvm(8)](http://man7.org/linux/man-pages/man8/lvm.8.html) and
[dmsetup(8)](http://man7.org/linux/man-pages/man8/dmsetup.8.html) for more information.

## How to run snapshotters benchmark

- `containerd` project contains AWS CloudFormation template to run an EC2 instance suitable for benchmarking.
It installs dependencies, prepares EBS volumes with same performance characteristics, and creates thin-pool device.
You can make stack with the following command (note: there is a charge for using AWS resources):

```bash
aws cloudformation create-stack \
    --stack-name benchmark-instance \
    --template-body file://benchmark_aws.yml \
    --parameters \
        ParameterKey=Key,ParameterValue=SSH_KEY \
        ParameterKey=SecurityGroups,ParameterValue=sg-XXXXXXXX \
        ParameterKey=VolumesSize,ParameterValue=20 \
        ParameterKey=VolumesIOPS,ParameterValue=1000
```

- You can find an IP address of newly created EC2 instance in AWS Console or via AWS CLI:

```bash
$ aws ec2 describe-instances \
    --instance-ids $(aws cloudformation describe-stack-resources --stack-name benchmark-instance --query 'StackResources[*].PhysicalResourceId' --output text) \
    --query 'Reservations[*].Instances[*].PublicIpAddress' \
    --output text
```

- SSH to an instance and prepare `containerd`:

```bash
ssh -i SSH_KEY ec2-user@IP
mkdir /mnt/disk1/data /mnt/disk2/data /mnt/disk3/data
go get github.com/containerd/containerd
cd $(go env GOPATH)/src/github.com/containerd/containerd
make
```

- Now you're ready to run the benchmark:

```bash
sudo su -
cd snapshots/benchsuite/
go test -bench . \
    -dm.thinPoolDev=bench-docker--pool \
    -dm.rootPath=/mnt/disk1/data \
    -overlay.rootPath=/mnt/disk2/data \
    -native.rootPath=/mnt/disk3/data
```

- The output will look like:

```bash
goos: linux
goarch: amd64
pkg: github.com/containerd/containerd/snapshots/testsuite

BenchmarkOverlay/run-4             1       1019730210 ns/op	 164.53 MB/s
BenchmarkOverlay/prepare           1         26799447 ns/op
BenchmarkOverlay/write             1        968200363 ns/op
BenchmarkOverlay/commit            1         24582560 ns/op

BenchmarkDeviceMapper/run-4        1       3139232730 ns/op	  53.44 MB/s
BenchmarkDeviceMapper/prepare	   1       1758640440 ns/op
BenchmarkDeviceMapper/write        1       1356705388 ns/op
BenchmarkDeviceMapper/commit       1         23720367 ns/op

PASS
ok  	github.com/containerd/containerd/snapshots/testsuite	185.204s
```

- Don't forget to tear down the stack so it does not continue to incur charges:

```bash
aws cloudformation delete-stack --stack-name benchmark-instance
```
