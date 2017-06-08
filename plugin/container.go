package plugin

import "context"

type TaskInfo struct {
	ID          string
	ContainerID string
	Runtime     string
	Spec        []byte
	Namespace   string
}

type Task interface {
	// Information of the container
	Info() TaskInfo
	// Start the container's user defined process
	Start(context.Context) error
	// State returns the container's state
	State(context.Context) (State, error)
	// Pause pauses the container process
	Pause(context.Context) error
	// Resume unpauses the container process
	Resume(context.Context) error
	// Kill signals a container
	Kill(context.Context, uint32, uint32, bool) error
	// Exec adds a process into the container
	Exec(context.Context, ExecOpts) (Process, error)
	// Processes returns all pids for the container
	Processes(context.Context) ([]uint32, error)
	// Pty resizes the processes pty/console
	Pty(context.Context, uint32, ConsoleSize) error
	// CloseStdin closes the processes stdin
	CloseStdin(context.Context, uint32) error
	// Checkpoint checkpoints a container to an image with live system data
	Checkpoint(context.Context, CheckpointOpts) error
	// DeleteProcess deletes a specific exec process via the pid
	DeleteProcess(context.Context, uint32) (*Exit, error)
}

type CheckpointOpts struct {
	Exit             bool
	AllowTCP         bool
	AllowUnixSockets bool
	AllowTerminal    bool
	FileLocks        bool
	EmptyNamespaces  []string
	Path             string
}

type ExecOpts struct {
	Spec []byte
	IO   IO
}

type Process interface {
	// State returns the process state
	State(context.Context) (State, error)
	// Kill signals a container
	Kill(context.Context, uint32, bool) error
}

type ConsoleSize struct {
	Width  uint32
	Height uint32
}

type Status int

const (
	CreatedStatus Status = iota + 1
	RunningStatus
	StoppedStatus
	DeletedStatus
	PausedStatus
)

type State struct {
	// Status is the current status of the container
	Status Status
	// Pid is the main process id for the container
	Pid      uint32
	Stdin    string
	Stdout   string
	Stderr   string
	Terminal bool
}
