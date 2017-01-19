package execution

type Status string

const (
	Created Status = "created"
	Paused  Status = "paused"
	Pausing Status = "pausing"
	Running Status = "running"
	Stopped Status = "stopped"
	Deleted Status = "deleted"
	Unknown Status = "unknown"

	UnknownStatusCode = 255
)
