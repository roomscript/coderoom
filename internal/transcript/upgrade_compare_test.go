package transcript

import "testing"

func TestCompareUpgradeOutputs_ReasoningSignals(t *testing.T) {
	tests := []struct {
		name     string
		previous ReasoningExpectation
		current  ReasoningExpectation
		wantErr  bool
	}{
		{"empty items disappear", ReasoningExpectation{NumMessages: 2, AllFlushed: true}, ReasoningExpectation{AllFlushed: true}, false},
		{"text disappears", ReasoningExpectation{NumMessages: 1, Content: "think"}, ReasoningExpectation{NumMessages: 1}, true},
		{"streams disappear", ReasoningExpectation{NumMessages: 1, NumStreams: 1}, ReasoningExpectation{NumMessages: 1}, true},
		{"stream flushing regresses", ReasoningExpectation{NumMessages: 1, NumStreams: 1, AllFlushed: true}, ReasoningExpectation{NumMessages: 1, NumStreams: 1}, true},
		{"messages disappear with text retained", ReasoningExpectation{NumMessages: 1, Content: "think"}, ReasoningExpectation{Content: "think"}, true},
		{"messages disappear with streams retained", ReasoningExpectation{NumMessages: 1, NumStreams: 1}, ReasoningExpectation{NumStreams: 1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := Output{Expect: Expect{Reasoning: tt.previous}}
			current := Output{Expect: Expect{Reasoning: tt.current}}
			if err := CompareUpgradeOutputs(previous, current); (err != nil) != tt.wantErr {
				t.Errorf("CompareUpgradeOutputs error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCompareUpgradeOutputs_AcceptsBroadlyEquivalentSignals(t *testing.T) {
	previous := Output{
		Expect: Expect{
			Output:     TextExpectation{NumMessages: 2, Content: "hello"},
			Log:        TextExpectation{NumMessages: 1, Content: "warning"},
			Reasoning:  ReasoningExpectation{NumMessages: 3, Content: "think", NumStreams: 1, AllFlushed: true},
			FileChange: FileChangeExpectation{NumMessages: 1, Files: []string{"a.txt"}},
			Command:    CommandExpectation{NumMessages: 1, Executed: []string{"echo hello"}},
			Notice:     &NoticeExpectation{NumTurnFlushes: 1},
			Approvals:  []ApprovalExpectation{{}},
		},
	}
	current := Output{
		Expect: Expect{
			Output:     TextExpectation{NumMessages: 5, Content: "different"},
			Log:        TextExpectation{NumMessages: 2, Content: "different warning"},
			Reasoning:  ReasoningExpectation{NumMessages: 1, Content: "also different", NumStreams: 2, AllFlushed: true},
			FileChange: FileChangeExpectation{NumMessages: 2, Files: []string{"b.txt"}},
			Command:    CommandExpectation{NumMessages: 1, Executed: []string{"pwd"}},
			Notice:     &NoticeExpectation{NumTurnFlushes: 1},
			Approvals:  []ApprovalExpectation{{}},
		},
	}

	if err := CompareUpgradeOutputs(previous, current); err != nil {
		t.Fatalf("CompareUpgradeOutputs: %v", err)
	}
}

func TestCompareUpgradeOutputs_RejectsDroppedSignals(t *testing.T) {
	previous := Output{
		Expect: Expect{
			Output:     TextExpectation{NumMessages: 1, Content: "hello"},
			Log:        TextExpectation{NumMessages: 1, Content: "warning"},
			Reasoning:  ReasoningExpectation{NumMessages: 1, Content: "think", NumStreams: 1, AllFlushed: true},
			FileChange: FileChangeExpectation{NumMessages: 1, Files: []string{"a.txt"}},
			Command:    CommandExpectation{NumMessages: 1, Executed: []string{"echo hello"}},
			Notice:     &NoticeExpectation{NumTurnFlushes: 1},
			Approvals:  []ApprovalExpectation{{}},
		},
	}
	current := Output{}

	if err := CompareUpgradeOutputs(previous, current); err == nil {
		t.Fatal("CompareUpgradeOutputs succeeded, want error")
	}
}
