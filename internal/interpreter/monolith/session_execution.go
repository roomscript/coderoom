package monolith

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// Session requests are concrete execution details, dispatched synchronously.
type sessionRequest interface{ sessionRequest() }

type createPlanAndExecuteParticipantSendRequest struct {
	alias   participant.Alias
	message string
	notice  string
}

type executePlannedParticipantSendRequest struct {
	plan    session.ParticipantSendPlan
	message string
	notice  string
}

type broadcastRequest struct {
	aliases []participant.Alias
	text    string
}

type cancelRequest struct{ alias participant.Alias }

type handoffRequest struct {
	fromAlias            participant.Alias
	toAlias              participant.Alias
	requiredReadyAliases []participant.Alias
	source               session.HandoffSource
}

func (createPlanAndExecuteParticipantSendRequest) sessionRequest() {}
func (executePlannedParticipantSendRequest) sessionRequest()       {}
func (broadcastRequest) sessionRequest()                           {}
func (cancelRequest) sessionRequest()                              {}
func (handoffRequest) sessionRequest()                             {}

func (e *interpreterExecutor) executeSessionRequest(request sessionRequest) error {
	switch request := request.(type) {
	case createPlanAndExecuteParticipantSendRequest:
		err := e.session.Execute(session.SendToParticipantCommand{
			Plan:    e.session.CreateParticipantSendPlan(request.alias),
			Message: request.message,
			Notice:  request.notice,
		})
		if err != nil {
			return fmt.Errorf("execute shared send: %w", err)
		}
		return nil
	case executePlannedParticipantSendRequest:
		err := e.session.Execute(session.SendToParticipantCommand{
			Plan:    request.plan,
			Message: request.message,
			Notice:  request.notice,
		})
		if err != nil {
			return fmt.Errorf("execute planned shared send: %w", err)
		}
		return nil
	case broadcastRequest:
		return e.executeBroadcastRequest(request)
	case cancelRequest:
		return e.executeCancelRequest(request)
	case handoffRequest:
		if err := e.session.Execute(session.HandoffCommand{
			FromAlias: request.fromAlias, ToAlias: request.toAlias,
			RequiredReadyAliases: slices.Clone(request.requiredReadyAliases), Source: request.source,
		}); err != nil {
			return fmt.Errorf("execute handoff: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported session request %T", request)
	}
}

func (e *interpreterExecutor) executeBroadcastRequest(request broadcastRequest) error {
	if err := e.session.Execute(session.BroadcastCommand{
		Aliases: slices.Clone(request.aliases), Text: request.text,
	}); err != nil {
		return fmt.Errorf("execute broadcast: %w", err)
	}
	return nil
}

func (e *interpreterExecutor) executeCancelRequest(request cancelRequest) error {
	if err := e.session.Execute(session.CancelCommand{Alias: request.alias}); err != nil {
		return fmt.Errorf("cancel %q: %w", request.alias.Name, err)
	}
	return nil
}
