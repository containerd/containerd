package ocicni

type cniNoOp struct {
}

func (noop *cniNoOp) Name() string {
	return "CNINoOp"
}

func (noop *cniNoOp) SetUpPod(netnsPath string, namespace string, name string, containerID string) error {
	return nil
}

func (noop *cniNoOp) TearDownPod(netnsPath string, namespace string, name string, containerID string) error {
	return nil
}

func (noop *cniNoOp) GetContainerNetworkStatus(netnsPath string, namespace string, name string, containerID string) (string, error) {
	return "", nil
}

func (noop *cniNoOp) Status() error {
	return nil
}
