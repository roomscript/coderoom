package interpreter

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

type stagedInvocation struct {
	id                 uint64
	source             promptlang.ParsedStatement
	description        *runtime.Staging
	cancelled          map[participant.Alias]bool
	interruptRequested bool
}

func (i *Interpreter) retainStage(id uint64, source promptlang.ParsedStatement, description *runtime.Staging) {
	if description == nil {
		return
	}
	i.stage = &stagedInvocation{id: id, source: source, description: description, cancelled: make(map[participant.Alias]bool)}
	i.fallback.PublishState()
}

func (i *Interpreter) updateStage(id uint64) {
	if i.stage != nil && i.stage.id == id {
		i.fallback.PublishState()
	}
}

// Projection reads the lifecycle's collected facts, never a fresh Session read.
func (i *Interpreter) stageSnapshot() *StagedSubmission {
	if i.stage == nil {
		return nil
	}
	stage := i.stage
	views, waiting := i.invocations[stage.id].lifecycle.Readiness()
	snapshot := &StagedSubmission{Raw: stage.source.Raw, Phase: StagePhasePending, InterruptRequested: stage.interruptRequested}
	unavailable := make(map[participant.Alias]bool)
	for _, view := range views {
		if view.Status == participant.StatusCrashed {
			unavailable[view.Alias] = true
			snapshot.Unavailable = append(snapshot.Unavailable, view.Alias.Name)
			continue
		}
		if !view.IsReadyForWork() {
			snapshot.NotReadyAliases = append(snapshot.NotReadyAliases, view.Alias.Name)
		}
		if waiting && stage.canInterrupt(view) {
			snapshot.Interruptible = append(snapshot.Interruptible, view.Alias.Name)
		}
	}
	for _, alias := range stagingRouting(stage.description) {
		if !unavailable[alias] {
			snapshot.Routing = append(snapshot.Routing, alias.Name)
		}
	}
	sort.Strings(snapshot.NotReadyAliases)
	sort.Strings(snapshot.Interruptible)
	sort.Strings(snapshot.Unavailable)
	return snapshot
}

func (i *Interpreter) rejectStagedInput(raw string, statement promptlang.ParsedStatement) bool {
	if i.stage == nil {
		return false
	}
	switch statement.Value.(type) {
	case promptlang.DebugView, promptlang.DebugRows:
		return false
	}
	i.fallback.Publish(InputRejected{Raw: raw, Code: ErrorStagePending, Err: &promptlang.Diagnostic{
		Code: promptlang.DiagnosticCode(ErrorStagePending), Span: promptlang.Span{End: len(raw)},
		Message: ErrStagePending.Error(), Cause: ErrStagePending,
	}})
	return true
}

func (i *Interpreter) finishStage(id uint64, result runtime.CommandResult) (runtime.CommandResult, []Event) {
	if i.stage == nil || i.stage.id != id {
		return result, nil
	}
	stage := i.stage
	i.stage = nil
	for _, record := range result.Records {
		if record.Kind == room.KindUserInput && len(record.Routing) != 0 {
			return result, []Event{StagedInputDispatched{Raw: stage.source.Raw, Routing: slices.Clone(record.Routing)}}
		}
	}
	var unavailable *runtime.RequiredParticipantUnavailableError
	if !errors.As(result.Err, &unavailable) && !errors.Is(result.Err, runtime.ErrNoActiveTargets) {
		return result, nil
	}
	views, _ := i.invocations[id].lifecycle.Readiness()
	var alias participant.Alias
	if unavailable != nil {
		alias = unavailable.Alias
	}
	_, handoff := stage.source.Value.(promptlang.Handoff)
	message, reason := unavailableStageMessages(alias, views, handoff)

	i.fallback.AppendRecord(room.Record{Kind: room.KindSystem, Text: message})
	// Departure discards already-accepted waiting work; it is not a second
	// submission failure or an execution failure after actual delivery.
	result.Err = nil
	return result, []Event{StagedInputDiscarded{Raw: stage.source.Raw, Reason: reason}}
}

func unavailableStageMessages(alias participant.Alias, views []participant.View, handoff bool) (string, string) {
	active := slices.ContainsFunc(views, func(view participant.View) bool {
		return view.Status != participant.StatusCrashed
	})
	if !active && !handoff {
		return "staged submission discarded: no active targets", "staged message discarded: no active targets"
	}
	return fmt.Sprintf("staged submission discarded: %q is no longer available", alias.Name), fmt.Sprintf("staged message discarded: %q is no longer available", alias.Name)
}
