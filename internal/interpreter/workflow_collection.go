package interpreter

// workflowCollection holds pending work; core_events.go owns event/result routing.
type workflowCollection struct {
	loop  loopWorkflow
	stage stageWorkflow
}

type workflowKind uint8

const (
	workflowLoop workflowKind = iota + 1
	workflowStage
)

type workflowRef struct {
	kind       workflowKind
	generation uint64
	requestID  uint64
}
