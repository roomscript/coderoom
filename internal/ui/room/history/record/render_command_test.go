package record

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
)

func ptr(v int) *int { return &v }

func TestRenderCommand_headerOnly(t *testing.T) {
	r := Record{
		Kind:  KindCommand,
		Alias: "bot",
		Msg: &agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content:  agent.Command{Command: "ls", Cwd: "/tmp"},
		},
	}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}}))
	if !strings.HasPrefix(out, "● bot:\n\n  $ ls") {
		t.Errorf("expected header starting with participant prefix and command line, got %q", out)
	}
}

func TestRenderCommand_withOutputAndExitCode(t *testing.T) {
	r := Record{
		Kind:  KindCommand,
		Alias: "bot",
		Msg: &agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content:  agent.Command{Command: "echo hi", Cwd: "/", Output: "hi\n", ExitCode: ptr(0)},
		},
	}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}}))
	if !strings.Contains(out, "echo hi") {
		t.Errorf("expected command in output, got %q", out)
	}
	if !strings.Contains(out, "\n\n  hi") {
		t.Errorf("expected output preview in render, got %q", out)
	}
	if strings.Contains(out, "Ctrl+O history") {
		t.Errorf("expected no navigation hint when nothing is hidden, got %q", out)
	}
	if !strings.Contains(out, "exit 0") {
		t.Errorf("expected exit code in render, got %q", out)
	}
}

func TestRenderCommand_outputPreview_showsTopThreeLinesAndHint(t *testing.T) {
	r := Record{
		Kind:  KindCommand,
		Alias: "bot",
		Msg: &agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content:  agent.Command{Command: "echo stuff", Cwd: "/", Output: "a\nb\nc\nd\n", ExitCode: ptr(0)},
		},
	}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}}))
	if !strings.Contains(out, "\n\n  a\n  b\n  c") {
		t.Errorf("expected top 3 output lines in preview, got %q", out)
	}
	if strings.Contains(out, "\n  d") {
		t.Errorf("expected preview to omit remaining lines, got %q", out)
	}
	if !strings.Contains(out, "(+1 more; Ctrl+O history, Ctrl+G open transcript)") {
		t.Errorf("expected navigation hint for more lines, got %q", out)
	}
}

func TestRenderCommand_longCmd_truncatesToSingleLine(t *testing.T) {
	r := Record{
		Kind:  KindCommand,
		Alias: "bot",
		Msg: &agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content:  agent.Command{Command: "abcdefghij", Cwd: "/tmp"},
		},
	}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 10}}))
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected header + blank + command line, got %q", out)
	}
	if strings.Contains(lines[2], "\n") {
		t.Fatalf("expected single command line, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "…") {
		t.Errorf("expected truncated command to include ellipsis, got %q", lines[2])
	}
}

func TestRenderCommandTranscript_indentsAllOutputLines(t *testing.T) {
	r := Record{
		Kind:  KindCommand,
		Alias: "bot",
		Msg: &agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content:  agent.Command{Command: "echo hi", Cwd: "/", Output: "line1\nline2\nline3\n", ExitCode: ptr(0)},
		},
	}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderTranscript}}))
	if !strings.Contains(out, "\n\n  line1\n  line2\n  line3") {
		t.Errorf("expected all output lines to be indented, got %q", out)
	}
}
