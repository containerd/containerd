package runtime

const (
	TaskCreateEventTopic       = "/tasks/create"
	TaskStartEventTopic        = "/tasks/start"
	TaskOOMEventTopic          = "/tasks/oom"
	TaskExitEventTopic         = "/tasks/exit"
	TaskDeleteEventTopic       = "/tasks/delete"
	TaskExecAddedEventTopic    = "/tasks/exec-added"
	TaskPausedEventTopic       = "/tasks/paused"
	TaskResumedEventTopic      = "/tasks/resumed"
	TaskCheckpointedEventTopic = "/tasks/checkpointed"
)
