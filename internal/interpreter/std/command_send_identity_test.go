package std_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestSendCommand_resolvesTargetThroughSession(t *testing.T) {
	delivery := newSendDelivery(t, false)
	current := delivery.frozenAlias("ada")
	stale := newTestAlias("ada")
	invocation, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada work"), runtime.Context{
		Session: delivery,
		Participants: participantReader{views: []participant.View{
			{Alias: stale, Status: participant.StatusCrashed},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer invocation.Close()
	if len(delivery.plans) != 1 || delivery.plans[0].PrimaryParticipant() != current || !slices.Equal(invocation.Staging().WaitFor, []participant.Alias{current}) {
		t.Fatal("preparation retained a snapshot name instead of Session's resolved alias")
	}
	if err := invocation.Go(func(runtime.CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if delivery.command.Plan.PrimaryParticipant() != current || len(delivery.plans) != 1 {
		t.Fatal("launch resolved the input name again")
	}
}

func TestSendCommand_rejectsTerminalRoutingForSameNameReplacement(t *testing.T) {
	delivery := newSendDelivery(t, false)
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	defer invocation.Close()
	original := invocation.Staging().WaitFor[0]
	replacement := newTestAlias(original.Name)
	var completions []runtime.CommandResult
	if err := invocation.Go(func(result runtime.CommandResult) { completions = append(completions, result) }); err != nil {
		t.Fatal(err)
	}
	result := session.RoutingResult{Kind: session.RoutingParticipantSend, Recipients: []session.RecipientResult{
		{Alias: replacement, Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
	}}
	delivery.complete(result, nil)
	if len(completions) != 1 || !errors.Is(completions[0].Err, runtime.ErrInvalidDeliveryOutcome) || len(completions[0].Records) != 0 {
		t.Fatalf("replacement outcome = %#v", completions)
	}
	result.Recipients[0].Alias = original
	delivery.complete(result, nil)
	if len(completions) != 1 {
		t.Fatal("invalid outcome left invocation waiting")
	}

}

func TestSendCommand_discardRequiresExactNoticeInstance(t *testing.T) {
	delivery := newSendDelivery(t, true)
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	defer invocation.Close()
	staging := invocation.Staging()
	original := staging.ToInform[0]
	replacement := newTestAlias(original.Name)
	staging.DiscardInform(replacement)
	if err := invocation.Go(func(runtime.CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delivery.command.Plan.Targets(), delivery.plans[0].Targets()) || !slices.Equal(staging.ToInform, []participant.Alias{original}) {
		t.Fatal("another instance's departure removed the original notice recipient")
	}
}
