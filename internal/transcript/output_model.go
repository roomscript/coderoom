package transcript

import "github.com/roomscript/coderoom/internal/agent"

// Output is the on-disk transcript fixture: front matter plus replay steps.
type Output struct {
	Name                  string   `yaml:"name"`
	CodexVersion          string   `yaml:"codex_version,omitempty"`
	Model                 string   `yaml:"model,omitempty"`
	DeveloperInstructions string   `yaml:"developer_instructions,omitempty"`
	Input                 string   `yaml:"input,omitempty"`
	Actions               []Action `yaml:"actions,omitempty"`
	Expect                Expect   `yaml:"expect"`
}

// File is a compatibility alias for the recorded transcript fixture.
type File = Output

// NormalizedActions returns the recorded action list, or a legacy single
// prompt action when older fixtures only carry Input.
func NormalizedActions(output Output) []Action {
	if len(output.Actions) > 0 {
		return output.Actions
	}
	if output.Input == "" {
		return nil
	}
	return []Action{{Kind: "prompt", Text: output.Input}}
}

// DefaultNoticeTurnFlushes returns the backward-compatible notice flush count
// implied by the normalized action list.
func DefaultNoticeTurnFlushes(output Output) int {
	count := 0
	for _, action := range NormalizedActions(output) {
		if action.Kind == "notice" {
			count++
		}
	}
	return count
}

// Expect stores the high-level expectations captured during recording.
type Expect struct {
	Output     TextExpectation       `yaml:"output"`
	Log        TextExpectation       `yaml:"log"`
	Reasoning  ReasoningExpectation  `yaml:"reasoning"`
	FileChange FileChangeExpectation `yaml:"file_change"`
	Command    CommandExpectation    `yaml:"command"`
	Notice     *NoticeExpectation    `yaml:"notice,omitempty"`
	Approvals  []ApprovalExpectation `yaml:"approvals"`
}

// TextExpectation summarizes a text-producing message category.
type TextExpectation struct {
	NumMessages int    `yaml:"num_messages"`
	Content     string `yaml:"content"`
}

// ReasoningExpectation summarizes reasoning messages and stream lifecycle.
type ReasoningExpectation struct {
	NumMessages int    `yaml:"num_messages"`
	Content     string `yaml:"content"`
	NumStreams  int    `yaml:"num_streams"`
	AllFlushed  bool   `yaml:"all_flushed"`
}

// FileChangeExpectation summarizes file-change messages observed in a turn.
type FileChangeExpectation struct {
	NumMessages int      `yaml:"num_messages"`
	Files       []string `yaml:"files"`
}

// CommandExpectation summarizes command messages observed in a turn.
type CommandExpectation struct {
	NumMessages int      `yaml:"num_messages"`
	Executed    []string `yaml:"executed"`
}

// NoticeExpectation summarizes synthetic notice lifecycle signals.
type NoticeExpectation struct {
	NumTurnFlushes int `yaml:"num_turn_flushes"`
}

// ApprovalExpectation records one normalized approval request/decision pair.
type ApprovalExpectation struct {
	Kind     agent.ApprovalKind   `yaml:"kind"`
	Decision agent.ApprovalOption `yaml:"decision"`
}

// Step is one replayable protocol action in the transcript script.
type Step struct {
	Kind    string `json:"kind"`
	Message any    `json:"message,omitempty"`
	Match   any    `json:"match,omitempty"`
	DelayMS int    `json:"delay_ms,omitempty"`
}
