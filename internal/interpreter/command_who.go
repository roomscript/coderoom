package interpreter

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
)

func (*interpreterModel) submitWho(raw string) instructionSequence {
	return append(acceptedInputSequence(raw), readParticipantsInstruction{raw: raw})
}

// participantViews supplies already-read facts, with no live session access.
type participantViews []participant.View

func (views participantViews) Participants() []participant.View { return views }

// participantsResultSequence is the experimental /who bridge. Legacy dispatch
// and participant reads stay intact while we prove the new invocation lifecycle.
// Generic module dispatch waits until asynchronous execution is designed.
func participantsResultSequence(result participantsResult) instructionSequence {
	records, err := (runtime.CommandRunner{}).Run(std.WhoCommand{}, runtime.Context{
		Participants: participantViews(result.participants),
	})
	sequence := make(instructionSequence, 0, len(records)+2)
	for _, record := range records {
		sequence = append(sequence, appendRecordInstruction{record: record})
	}
	sequence.append(submissionResultSequence(submissionOutcome{
		raw: result.raw, operation: "who", statement: result.statement, err: err,
	}))
	return sequence
}
