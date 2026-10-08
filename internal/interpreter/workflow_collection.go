package interpreter

// workflowCollection holds pending work; core_events.go owns event/result routing.
type workflowCollection struct {
	loop  loopWorkflow
	stage stageWorkflow
}
