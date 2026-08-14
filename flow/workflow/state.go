package workflow

// TaskState is the state of a workflow task node for one instance.
// Mirrors Java's TaskState.
type TaskState int

const (
	TaskStateUnknown    TaskState = 0
	TaskStateWaiting    TaskState = 1001
	TaskStateCompleted  TaskState = 1002
	TaskStateTerminated TaskState = 1003
)

// Code returns the numeric code.
func (s TaskState) Code() int { return int(s) }

// String returns the canonical name.
func (s TaskState) String() string {
	switch s {
	case TaskStateWaiting:
		return "WAITING"
	case TaskStateCompleted:
		return "COMPLETED"
	case TaskStateTerminated:
		return "TERMINATED"
	}
	return "UNKNOWN"
}

// TaskStateOf parses a state from its code.
func TaskStateOf(code int) TaskState {
	switch code {
	case 1001:
		return TaskStateWaiting
	case 1002:
		return TaskStateCompleted
	case 1003:
		return TaskStateTerminated
	}
	return TaskStateUnknown
}

// TaskAction is a workflow submit operation. Mirrors Java's TaskAction.
type TaskAction int

const (
	ActionUnknown     TaskAction = 0
	ActionBack        TaskAction = 1010 // => WAITING
	ActionBackJump    TaskAction = 1011 // => WAITING
	ActionForward     TaskAction = 1020 // => COMPLETED
	ActionForwardJump TaskAction = 1021 // => COMPLETED
	ActionTerminate   TaskAction = 1030 // => TERMINATED
	ActionRestart     TaskAction = 1040 // => UNKNOWN (clear)
)

// Code returns the numeric code.
func (a TaskAction) Code() int { return int(a) }

// TargetState returns the state the action transitions a node to.
func (a TaskAction) TargetState() TaskState {
	switch a {
	case ActionBack, ActionBackJump:
		return TaskStateWaiting
	case ActionForward, ActionForwardJump:
		return TaskStateCompleted
	case ActionTerminate:
		return TaskStateTerminated
	case ActionRestart:
		return TaskStateUnknown
	}
	return TaskStateUnknown
}

// TaskActionOf parses an action from its code.
func TaskActionOf(code int) TaskAction {
	switch code {
	case 1010:
		return ActionBack
	case 1011:
		return ActionBackJump
	case 1020:
		return ActionForward
	case 1021:
		return ActionForwardJump
	case 1030:
		return ActionTerminate
	case 1040:
		return ActionRestart
	}
	return ActionUnknown
}
