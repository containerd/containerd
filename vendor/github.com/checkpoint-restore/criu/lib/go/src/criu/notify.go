package criu

type CriuNotify interface {
	PreDump() error
	PostDump() error
	PreRestore() error
	PostRestore(pid int32) error
	NetworkLock() error
	NetworkUnlock() error
	SetupNamespaces(pid int32) error
	PostSetupNamespaces() error
	PostResume() error
}

type CriuNoNotify struct {
}

func (c CriuNoNotify) PreDump() error {
	return nil
}

func (c CriuNoNotify) PostDump() error {
	return nil
}

func (c CriuNoNotify) PreRestore() error {
	return nil
}

func (c CriuNoNotify) PostRestore(pid int32) error {
	return nil
}

func (c CriuNoNotify) NetworkLock() error {
	return nil
}

func (c CriuNoNotify) NetworkUnlock() error {
	return nil
}

func (c CriuNoNotify) SetupNamespaces(pid int32) error {
	return nil
}

func (c CriuNoNotify) PostSetupNamespaces() error {
	return nil
}

func (c CriuNoNotify) PostResume() error {
	return nil
}
