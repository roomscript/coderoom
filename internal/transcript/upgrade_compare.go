package transcript

import (
	"errors"
	"fmt"
	"strings"
)

// CompareUpgradeOutputs validates that a newly recorded transcript preserves
// the broad behavioral signals present in a previous version's transcript.
func CompareUpgradeOutputs(previous, current Output) error {
	var problems []string

	compareTextExpectation(&problems, "output", previous.Expect.Output, current.Expect.Output)
	compareTextExpectation(&problems, "log", previous.Expect.Log, current.Expect.Log)
	compareReasoningExpectation(&problems, previous.Expect.Reasoning, current.Expect.Reasoning)
	compareMessageCount(&problems, "file_change.num_messages", previous.Expect.FileChange.NumMessages, current.Expect.FileChange.NumMessages)
	compareNonEmptyList(&problems, "file_change.files", previous.Expect.FileChange.Files, current.Expect.FileChange.Files)
	compareMessageCount(&problems, "command.num_messages", previous.Expect.Command.NumMessages, current.Expect.Command.NumMessages)
	compareNonEmptyList(&problems, "command.executed", previous.Expect.Command.Executed, current.Expect.Command.Executed)
	compareNoticeExpectation(&problems, previous.Expect.Notice, current.Expect.Notice)
	compareNonEmptyApprovals(&problems, previous.Expect.Approvals, current.Expect.Approvals)

	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func compareTextExpectation(problems *[]string, label string, previous, current TextExpectation) {
	compareMessageCount(problems, label+".num_messages", previous.NumMessages, current.NumMessages)
	if previous.Content != "" && current.Content == "" {
		*problems = append(*problems, fmt.Sprintf("%s.content was non-empty and is now empty", label))
	}
}

func compareReasoningExpectation(problems *[]string, previous, current ReasoningExpectation) {
	// Empty reasoning items are optional protocol bookkeeping, not a signal
	// that must survive a model or version change.
	if previous.Content != "" || previous.NumStreams > 0 {
		compareMessageCount(problems, "reasoning.num_messages", previous.NumMessages, current.NumMessages)
	}
	if previous.Content != "" && current.Content == "" {
		*problems = append(*problems, "reasoning.content was non-empty and is now empty")
	}
	compareMessageCount(problems, "reasoning.num_streams", previous.NumStreams, current.NumStreams)
	if previous.NumStreams > 0 && previous.AllFlushed && !current.AllFlushed {
		*problems = append(*problems, "reasoning.all_flushed was true and is now false")
	}
}

func compareNoticeExpectation(problems *[]string, previous, current *NoticeExpectation) {
	if previous == nil || previous.NumTurnFlushes == 0 {
		return
	}
	if current == nil || current.NumTurnFlushes == 0 {
		*problems = append(*problems, "notice.num_turn_flushes was >0 and is now 0")
	}
}

func compareNonEmptyApprovals(problems *[]string, previous, current []ApprovalExpectation) {
	if len(previous) > 0 && len(current) == 0 {
		*problems = append(*problems, "approvals were non-empty and are now empty")
	}
}

func compareNonEmptyList(problems *[]string, label string, previous, current []string) {
	if len(previous) > 0 && len(current) == 0 {
		*problems = append(*problems, fmt.Sprintf("%s was non-empty and is now empty", label))
	}
}

func compareMessageCount(problems *[]string, label string, previous, current int) {
	if previous > 0 && current == 0 {
		*problems = append(*problems, fmt.Sprintf("%s was >0 and is now 0", label))
	}
}
