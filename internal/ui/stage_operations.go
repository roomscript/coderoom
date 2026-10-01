package ui

import tea "charm.land/bubbletea/v2"

type stageOperator interface {
	TakeStageForEdit() (string, bool)
	DiscardStage() bool
	InterruptAndDispatchStage() bool
}

type stageTakenForEditMsg struct {
	raw string
	ok  bool
}

type stageDiscardedMsg struct{ ok bool }

type stageInterruptRequestedMsg struct{ ok bool }

func takeStageForEdit(operator stageOperator) tea.Cmd {
	return func() tea.Msg {
		raw, ok := operator.TakeStageForEdit()
		return stageTakenForEditMsg{raw: raw, ok: ok}
	}
}

func discardStage(operator stageOperator) tea.Cmd {
	return func() tea.Msg {
		return stageDiscardedMsg{ok: operator.DiscardStage()}
	}
}

func interruptAndDispatchStage(operator stageOperator) tea.Cmd {
	return func() tea.Msg {
		return stageInterruptRequestedMsg{ok: operator.InterruptAndDispatchStage()}
	}
}
