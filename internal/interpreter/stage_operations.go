package interpreter

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

type stageOperationResult struct {
	raw string
	ok  bool
}

func (i *Interpreter) runStageOperation(operation func() stageOperationResult) stageOperationResult {
	result := make(chan stageOperationResult, 1)
	if err := i.fallback.Schedule(func() { result <- operation() }); err != nil {
		return stageOperationResult{}
	}
	// Schedule guarantees admitted work resolves before shutdown closes the lane.
	return <-result
}

func (i *Interpreter) takeStageForEdit() stageOperationResult {
	if i.stage == nil {
		raw, ok := i.fallback.TakeLegacyStageForEdit()
		return stageOperationResult{raw: raw, ok: ok}
	}
	return i.removeWaitingStage()
}

func (i *Interpreter) discardStage() stageOperationResult {
	if i.stage == nil {
		return stageOperationResult{ok: i.fallback.DiscardLegacyStage()}
	}
	return i.removeWaitingStage()
}

func (i *Interpreter) removeWaitingStage() stageOperationResult {
	stage := i.stage
	pending := i.invocations[stage.id]
	if !pending.lifecycle.DiscardWaiting() {
		return stageOperationResult{}
	}
	pending.publication.submission.Close()
	delete(i.invocations, stage.id)
	i.stage = nil
	i.fallback.PublishState()
	return stageOperationResult{raw: stage.source.Raw, ok: true}
}

func (i *Interpreter) interruptStage() stageOperationResult {
	if i.stage == nil {
		return stageOperationResult{ok: i.fallback.InterruptLegacyStage()}
	}
	stage := i.stage
	pending := i.invocations[stage.id]
	views, waiting := pending.lifecycle.Readiness()
	if !waiting {
		return stageOperationResult{}
	}
	aliases := stage.interruptible(views)
	if len(aliases) == 0 {
		return stageOperationResult{}
	}
	stage.interruptRequested = true
	i.fallback.PublishState()
	for _, alias := range aliases {
		if err := pending.publication.submission.CancelParticipant(alias); err != nil {
			i.fallback.Publish(OperationFailed{Raw: stage.source.Raw, Statement: stage.source, Operation: "interrupt staged submission", Err: err})
			continue
		}
		stage.cancelled[alias] = true
		i.fallback.AppendRecord(room.Record{Kind: room.KindSystem, Text: fmt.Sprintf("[→ %s] interrupt requested", alias.Name)})
	}
	i.fallback.PublishState()
	return stageOperationResult{ok: true}
}

func (s *stagedInvocation) interruptible(views []participant.View) []participant.Alias {
	var aliases []participant.Alias
	for _, view := range views {
		if s.canInterrupt(view) {
			aliases = append(aliases, view.Alias)
		}
	}
	return aliases
}

func (s *stagedInvocation) canInterrupt(view participant.View) bool {
	return view.HasActiveTurn() && view.IsCancellable() && !s.cancelled[view.Alias]
}
