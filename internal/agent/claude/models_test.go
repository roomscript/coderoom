package claude

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

const lineInitCatalog = `{"type":"control_response","response":{"subtype":"success","request_id":"coderoom-init","response":{"models":[` +
	`{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default","supportsEffort":true,"supportedEffortLevels":["low","high"]},` +
	`{"value":"haiku","resolvedModel":"claude-haiku-4-5","displayName":"Haiku"},` +
	`{"value":"claude-sonnet-4-5"}]}}}`

var opus = agent.ModelOption{Name: "opus", ID: "claude-opus-5-5", Efforts: []string{"low", "high"}}

func controlSuccessLine(id, body string) string {
	return `{"type":"control_response","response":{"subtype":"success","request_id":"` + id + `","response":` + body + `}}`
}

func controlErrorLine(id, msg string) string {
	return `{"type":"control_response","response":{"subtype":"error","request_id":"` + id + `","error":"` + msg + `"}}`
}

func appliedBody(model, effort string) string {
	return `{"applied":{"model":"` + model + `","effort":"` + effort + `"}}`
}

func readModelMessage(t *testing.T, c *Client) agent.Message {
	t.Helper()
	msgs := readUntil(t, c, func(m agent.Message) bool { return m.StreamID == modelsStreamID })
	return msgs[len(msgs)-1]
}

func TestModels_startupCatalog(t *testing.T) {
	c := startPeer(t, peerScript([]string{lineInitCatalog}))

	want := []agent.ModelOption{
		{Name: "default", ID: "claude-opus-5-5", Efforts: []string{"low", "high"}},
		{Name: "haiku", ID: "claude-haiku-4-5"},
		{Name: "claude-sonnet-4-5", ID: "claude-sonnet-4-5"},
	}
	if err := c.Models(); err != nil {
		t.Fatal(err)
	}
	listed, ok := readModelMessage(t, c).Content.(agent.ModelsListed)
	if !ok || listed.Err != nil || !slices.EqualFunc(listed.Models, want, equalOption) {
		t.Errorf("listing = %+v, want %+v", listed, want)
	}
	if got := c.Settings(); got != (agent.Settings{Backend: "claude", Model: "claude-haiku-4-5"}) {
		t.Errorf("Settings = %+v, want haiku with no effort", got)
	}
}

func equalOption(a, b agent.ModelOption) bool {
	return a.Name == b.Name && a.ID == b.ID && slices.Equal(a.Efforts, b.Efforts)
}

func TestModels_startupSettingsFailure(t *testing.T) {
	// The peer answers get_settings with an error instead of the default
	// success.
	c := startPeer(t, "read -r _; printf '%s\\n' '"+lineInitOK+"'; read -r _; printf '%s\\n' '"+
		controlErrorLine(settingsRequestID, "unsupported")+"'; cat >/dev/null")

	if got := c.Settings(); got != (agent.Settings{Backend: "claude"}) {
		t.Errorf("Settings = %+v, want unknown model and effort", got)
	}
}

type selectModelTest struct {
	name         string
	effort       string
	answers      [][]string // after the handshake, one step per request
	wantSubtypes []string
	wantSettings agent.Settings
	wantErr      bool
}

func selectModelTests() []selectModelTest {
	return []selectModelTest{
		{"model only", "",
			[][]string{
				{controlSuccessLine("coderoom-set-model-1", "null")},
				{controlSuccessLine("coderoom-settings-2", appliedBody("claude-opus-5-5", "low"))},
			},
			[]string{controlSetModel, controlGetSettings},
			agent.Settings{Backend: "claude", Model: "claude-opus-5-5", Effort: "low"}, false},
		{"model and effort", "high",
			[][]string{
				{controlSuccessLine("coderoom-set-model-1", "null")},
				{controlSuccessLine("coderoom-effort-2", "null")},
				{controlSuccessLine("coderoom-settings-3", appliedBody("claude-opus-5-5", "high"))},
			},
			[]string{controlSetModel, controlApplyFlagSettings, controlGetSettings},
			agent.Settings{Backend: "claude", Model: "claude-opus-5-5", Effort: "high"}, false},
		{"set_model fails", "high",
			[][]string{
				{controlErrorLine("coderoom-set-model-1", "Model not found")},
				{controlSuccessLine("coderoom-settings-2", appliedBody("claude-haiku-4-5", "low"))},
			},
			[]string{controlSetModel, controlGetSettings},
			agent.Settings{Backend: "claude", Model: "claude-haiku-4-5", Effort: "low"}, true},
		{"readback fails", "",
			[][]string{
				{controlSuccessLine("coderoom-set-model-1", "null")},
				{controlErrorLine("coderoom-settings-2", "broken")},
			},
			[]string{controlSetModel, controlGetSettings},
			agent.Settings{Backend: "claude"}, true},
	}
}

func TestSelectModel_exchange(t *testing.T) {
	for _, tt := range selectModelTests() {
		t.Run(tt.name, func(t *testing.T) {
			obs := &recordingObserver{}
			c := startPeer(t, peerScript(append([][]string{{lineInitOK}}, tt.answers...)...), WithObserver(obs))
			before := len(obs.sentLines())

			if err := c.SelectModel(opus, tt.effort); err != nil {
				t.Fatal(err)
			}
			selected, ok := readModelMessage(t, c).Content.(agent.ModelSelected)
			if !ok || selected.Settings != tt.wantSettings || (selected.Err != nil) != tt.wantErr {
				t.Errorf("ModelSelected = %+v, want %+v (error %v)", selected, tt.wantSettings, tt.wantErr)
			}
			if got := c.Settings(); got != tt.wantSettings {
				t.Errorf("Settings = %+v, want %+v", got, tt.wantSettings)
			}
			checkExchangeRequests(t, obs.sentLines()[before:], tt.wantSubtypes, tt.effort)
		})
	}
}

// checkExchangeRequests checks the requests of one exchange: set_model with
// the model ID, the effort when given, and the readback.
func checkExchangeRequests(t *testing.T, sent, wantSubtypes []string, effort string) {
	t.Helper()
	if len(sent) != len(wantSubtypes) {
		t.Fatalf("sent %v, want subtypes %v", sent, wantSubtypes)
	}
	for i, line := range sent {
		req := parseSentControl(t, line)
		if req.Request.Subtype != wantSubtypes[i] {
			t.Errorf("request %d = %s, want %s", i, req.Request.Subtype, wantSubtypes[i])
		}
		if req.Request.Subtype == controlSetModel && req.Request.Model != opus.ID {
			t.Errorf("set_model model = %q, want the ID %q", req.Request.Model, opus.ID)
		}
		if req.Request.Subtype == controlApplyFlagSettings && req.Request.Settings["effortLevel"] != effort {
			t.Errorf("apply_flag_settings = %v, want effortLevel %q", req.Request.Settings, effort)
		}
	}
}

func TestSelectModel_holdsPromptGate(t *testing.T) {
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{controlSuccessLine("coderoom-set-model-1", "null")},
		[]string{}, // the readback stays unanswered until the next step
	))

	if err := c.SelectModel(opus, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send("hi"); !errors.Is(err, agent.ErrTurnInProgress) {
		t.Errorf("Send during a change = %v, want ErrTurnInProgress", err)
	}
	if _, err := c.SendNotice("hi"); !errors.Is(err, agent.ErrTurnInProgress) {
		t.Errorf("SendNotice during a change = %v, want ErrTurnInProgress", err)
	}
	if err := c.SelectModel(opus, ""); !errors.Is(err, agent.ErrTurnInProgress) {
		t.Errorf("SelectModel during a change = %v, want ErrTurnInProgress", err)
	}
}

func TestSelectModel_localCommandReplayLeavesClientIdle(t *testing.T) {
	// Claude echoes set_model as a replayed local command, with no turn.
	echo := replayLineID("<local-command-stdout>Set model to `claude-opus-5-5`</local-command-stdout>", "local-1")
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{echo, controlSuccessLine("coderoom-set-model-1", "null")},
		[]string{controlSuccessLine("coderoom-settings-2", appliedBody("claude-opus-5-5", "low"))},
	))

	if err := c.SelectModel(opus, ""); err != nil {
		t.Fatal(err)
	}
	readModelMessage(t, c)
	if err := c.SelectModel(opus, ""); err != nil {
		t.Errorf("SelectModel after a change = %v, want accepted", err)
	}
}

func TestSelectModel_refused(t *testing.T) {
	tests := []struct {
		name   string
		effort string
		busy   bool
	}{
		{"unsupported effort", "max", false},
		{"turn in progress", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &recordingObserver{}
			c := startPeer(t, peerScript([]string{lineInitOK}), WithObserver(obs))
			if tt.busy {
				if _, err := c.Send("hi"); err != nil {
					t.Fatal(err)
				}
			}
			before := len(obs.sentLines())
			if err := c.SelectModel(opus, tt.effort); err == nil {
				t.Fatal("expected error")
			}
			if n := len(obs.sentLines()) - before; n != 0 {
				t.Errorf("a refused change wrote %d lines", n)
			}
		})
	}
}

func TestSelectModel_processExitEndsExchange(t *testing.T) {
	c := startPeer(t, peerNext+"next; printf '%s\\n' '"+lineInitOK+"'; next; exit 0")

	if err := c.SelectModel(opus, ""); err != nil {
		t.Fatal(err)
	}
	readUntilExit(t, c)
	if _, err := c.Send("hi"); errors.Is(err, agent.ErrTurnInProgress) {
		t.Error("Send after exit still blocked by the change")
	}
}
