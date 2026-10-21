package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

type modelRequest struct {
	ID     int                        `json:"id"`
	Method string                     `json:"method"`
	Params map[string]json.RawMessage `json:"params"`
	Result map[string]json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

type modelWriter struct {
	requests chan modelRequest
	err      error
}

func (w *modelWriter) Write(raw []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	var request modelRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return 0, fmt.Errorf("decode fake request: %w", err)
	}
	w.requests <- request
	return len(raw), nil
}
func (*modelWriter) Close() error { return nil }

type modelPeer struct {
	c      *Client
	writer *modelWriter
	t      *testing.T
}

func newModelPeer(t *testing.T) *modelPeer {
	t.Helper()
	writer := &modelWriter{requests: make(chan modelRequest, 32)}
	c := New(".")
	c.proc.codexIn = writer
	c.turn.threadID = "t1"
	c.models.settings = agent.Settings{Backend: "codex", Model: "old", Effort: "high"}
	c.initRead()
	c.lifecycle.ctx, c.lifecycle.cancelFn = context.WithCancel(context.Background()) // #nosec: G118
	c.lifecycle.waitGroup.Go(func() { messageBufferWorker(c.lifecycle.ctx, c) })
	t.Cleanup(func() { c.lifecycle.cancelFn(); c.lifecycle.waitGroup.Wait() })
	return &modelPeer{c: c, writer: writer, t: t}
}
func (p *modelPeer) request(method string) modelRequest {
	p.t.Helper()
	select {
	case r := <-p.writer.requests:
		if r.Method != method {
			p.t.Fatalf("request=%s, want %s", r.Method, method)
		}
		if method == "model/list" && r.Params == nil {
			p.t.Fatal("model/list must send an explicit params object")
		}
		return r
	case <-time.After(time.Second):
		p.t.Fatal("request did not arrive")
		return modelRequest{}
	}
}
func (p *modelPeer) emit(raw string) {
	p.t.Helper()
	var msg rpcEnvelope
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		p.t.Fatal(err)
	}
	if !handleStdoutEnvelope(p.c.lifecycle.ctx, p.c, msg) {
		p.t.Fatal("envelope stopped worker")
	}
}
func (p *modelPeer) reply(id int, result string) {
	p.t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": id, "result": json.RawMessage(result)})
	p.emit(string(raw))
}
func (p *modelPeer) selected() agent.ModelSelected {
	p.t.Helper()
	msg, err := p.c.Read()
	if err != nil {
		p.t.Fatal(err)
	}
	result, ok := msg.Content.(agent.ModelSelected)
	if !ok || msg.Mode != agent.ModeSingle {
		p.t.Fatalf("message=%+v, want ModelSelected", msg)
	}
	return result
}
func (p *modelPeer) listed() agent.ModelsListed {
	p.t.Helper()
	msg, err := p.c.Read()
	if err != nil {
		p.t.Fatal(err)
	}
	result, ok := msg.Content.(agent.ModelsListed)
	if !ok || msg.Mode != agent.ModeSingle {
		p.t.Fatalf("message=%+v, want ModelsListed", msg)
	}
	return result
}

var modelTestOption = agent.ModelOption{Name: "new", ID: "new", Efforts: []string{"low", "high"}}

func TestModelSelection_readbackAndAdmission(t *testing.T) {
	for _, effort := range []string{"", "low"} {
		t.Run("effort="+effort, func(t *testing.T) {
			p := newModelPeer(t)
			if err := p.c.SelectModel(modelTestOption, effort); err != nil {
				t.Fatal(err)
			}
			request := p.request(methodTurnStart)
			assertModelRequest(t, request, effort)
			p.assertBusy()
			p.reply(request.ID, `{"turn":{"id":"u1"}}`)
			p.emit(turnStarted)
			p.emit(agentDelta("discard this"))
			p.emit(`{"method":"item/completed","params":{"threadId":"t1","item":{"type":"agentMessage","id":"a1"}}}`)
			p.emit(`{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u1","status":"completed"}}}`)
			read := p.request("thread/read")
			p.assertBusy()
			p.reply(read.ID, `{"thread":{"model":"resolved","reasoningEffort":"high"}}`)
			result := p.selected()
			want := agent.Settings{Backend: "codex", Model: "resolved", Effort: "high"}
			if result.Err != nil || result.Settings != want || p.c.Settings() != want {
				t.Fatalf("outcome=%+v settings=%+v", result, p.c.Settings())
			}
			if _, err := p.c.Send("next"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func (p *modelPeer) assertBusy() {
	p.t.Helper()
	if _, err := p.c.Send("hi"); !errors.Is(err, agent.ErrTurnInProgress) {
		p.t.Errorf("Send=%v", err)
	}
	if _, err := p.c.SendNotice("hi"); !errors.Is(err, agent.ErrTurnInProgress) {
		p.t.Errorf("SendNotice=%v", err)
	}
	if err := p.c.KeepAlive(); !errors.Is(err, agent.ErrTurnInProgress) {
		p.t.Errorf("KeepAlive=%v", err)
	}
	if err := p.c.SelectModel(modelTestOption, ""); !errors.Is(err, agent.ErrTurnInProgress) {
		p.t.Errorf("SelectModel=%v", err)
	}
}

func TestModelSelection_failuresKeepReadback(t *testing.T) {
	tests := []struct {
		name  string
		event string
	}{
		{"failed", `{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u1","status":"failed","error":{"message":"unavailable"}}}}`},
		{"interrupted", `{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u1","status":"interrupted"}}}`},
		{"legacy failure", `{"method":"turn/failed","params":{"threadId":"t1"}}`},
		{"malformed status", `{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u1"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newModelPeer(t)
			if err := p.c.SelectModel(modelTestOption, "low"); err != nil {
				t.Fatal(err)
			}
			p.request(methodTurnStart)
			p.emit(turnStarted)
			p.emit(tt.event)
			r := p.request("thread/read")
			p.reply(r.ID, `{"thread":{"model":"new","reasoningEffort":"low"}}`)
			result := p.selected()
			if result.Err == nil || result.Settings.Model != "new" || result.Settings.Effort != "low" {
				t.Fatalf("result=%+v", result)
			}
			if err := p.c.KeepAlive(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestModelSelection_startRejectedAndReadbackFails(t *testing.T) {
	p := newModelPeer(t)
	if err := p.c.SelectModel(modelTestOption, ""); err != nil {
		t.Fatal(err)
	}
	r := p.request(methodTurnStart)
	raw, _ := json.Marshal(map[string]any{"id": r.ID, "error": map[string]any{"code": -32600}})
	p.emit(string(raw))
	r = p.request("thread/read")
	p.reply(r.ID, `{}`)
	result := p.selected()
	if result.Err == nil || result.Settings != (agent.Settings{Backend: "codex"}) {
		t.Fatalf("result=%+v", result)
	}
	if _, err := p.c.Send("next"); err != nil {
		t.Fatal(err)
	}
}

func TestModelSelection_rejectsBeforeWrite(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*Client)
		effort  string
	}{
		{"invalid effort", func(*Client) {}, "bogus"},
		{"not started", func(c *Client) { c.turn.threadID = "" }, ""},
		{"pending turn", func(c *Client) { c.turn.state = turnState{kind: turnInflightUnknownID} }, ""},
		{"active turn", func(c *Client) { c.turn.state = turnState{kind: turnInflightKnownID, turnID: "u1"} }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newModelPeer(t)
			tt.prepare(p.c)
			if err := p.c.SelectModel(modelTestOption, tt.effort); err == nil {
				t.Fatal("expected rejection")
			}
			if len(p.writer.requests) != 0 {
				t.Fatal("rejected request was sent")
			}
		})
	}
}

func TestModelSelection_initialWriteFailureReleasesGate(t *testing.T) {
	p := newModelPeer(t)
	p.writer.err = io.ErrClosedPipe
	if err := p.c.SelectModel(modelTestOption, ""); err == nil {
		t.Fatal("expected write failure")
	}
	if p.c.Settings().Model != "old" {
		t.Fatal("initial write changed settings")
	}
	p.writer.err = nil
	if _, err := p.c.Send("next"); err != nil {
		t.Fatal(err)
	}
}

func TestModelCatalog_paginationAndOrderedOutcomes(t *testing.T) {
	p := newModelPeer(t)
	if _, err := p.c.Send("work"); err != nil {
		t.Fatal(err)
	}
	p.request(methodTurnStart)
	for range 2 {
		if err := p.c.Models(); err != nil {
			t.Fatal(err)
		}
	}
	first, second := p.request("model/list"), p.request("model/list")
	p.reply(second.ID, `{"data":[{"model":"second"}]}`)
	p.reply(first.ID, `{"data":[{"model":"first","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}],"nextCursor":"page2"}`)
	next := p.request("model/list")
	if string(next.Params["cursor"]) != `"page2"` {
		t.Fatalf("cursor=%s", next.Params["cursor"])
	}
	p.reply(next.ID, `{"data":[{"model":"last"}],"nextCursor":null}`)
	result := p.listed()
	want := []agent.ModelOption{{Name: "first", ID: "first", Efforts: []string{"low"}}, {Name: "last", ID: "last"}}
	if result.Err != nil || !reflect.DeepEqual(result.Models, want) {
		t.Fatalf("first=%+v", result)
	}
	if result = p.listed(); result.Err != nil || result.Models[0].ID != "second" {
		t.Fatalf("second=%+v", result)
	}
}

func TestModelCatalog_repeatedCursorAndMalformedPage(t *testing.T) {
	for _, page := range []string{`{}`, `{"data":[{}]}`, `{"data":null}`, `{"data":[],"nextCursor":"same"}`} {
		t.Run(page, func(t *testing.T) {
			p := newModelPeer(t)
			if err := p.c.Models(); err != nil {
				t.Fatal(err)
			}
			r := p.request("model/list")
			if page == `{"data":[],"nextCursor":"same"}` {
				p.reply(r.ID, page)
				r = p.request("model/list")
			}
			p.reply(r.ID, page)
			result := p.listed()
			if result.Err == nil || result.Models != nil {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestModelSettings_startupAndMissingFields(t *testing.T) {
	c := New(".")
	if c.Settings() != (agent.Settings{Backend: "codex"}) {
		t.Fatal(c.Settings())
	}
	c.proc.codexIn = &modelWriter{requests: make(chan modelRequest, 2)}
	c.proc.codexOut = bufio.NewReader(strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"model\":\"configured\",\"reasoningEffort\":\"medium\",\"thread\":{\"id\":\"t1\"}}}\n"))
	if _, err := rpcHandshake(c); err != nil {
		t.Fatal(err)
	}
	if c.Settings() != (agent.Settings{Backend: "codex", Model: "configured", Effort: "medium"}) {
		t.Fatal(c.Settings())
	}
	for _, raw := range []string{`{"thread":{}}`, `{"thread":{"model":null,"reasoningEffort":null}}`} {
		settings, err := parseModelSettings(json.RawMessage(raw), nil)
		if err != nil || settings != (agent.Settings{Backend: "codex"}) {
			t.Fatalf("settings=%+v error=%v", settings, err)
		}
	}
}

func assertModelRequest(t *testing.T, request modelRequest, effort string) {
	t.Helper()
	if string(request.Params["model"]) != `"new"` {
		t.Errorf("model=%s", request.Params["model"])
	}
	if effort == "" && request.Params["effort"] != nil {
		t.Error("model-only request sent effort")
	}
	if effort != "" && string(request.Params["effort"]) != `"low"` {
		t.Errorf("effort=%s", request.Params["effort"])
	}
}

func TestModelSelection_unexpectedActivityFailsConfirmation(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		approval bool
	}{
		{"tool", `{"method":"item/started","params":{"threadId":"t1","item":{"id":"tool","type":"commandExecution"}}}`, false},
		{"unknown item", `{"method":"item/started","params":{"threadId":"t1","item":{"id":"tool","type":"futureTool"}}}`, false},
		{"malformed item", `{"method":"item/started","params":{"threadId":"t1","item":null}}`, false},
		{"approval", `{"id":100,"method":"item/commandExecution/requestApproval","params":{"threadId":"t1","turnId":"u1","itemId":"tool","command":"pwd","cwd":"/tmp"}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newModelPeer(t)
			if err := p.c.SelectModel(modelTestOption, ""); err != nil {
				t.Fatal(err)
			}
			p.request(methodTurnStart)
			p.emit(turnStarted)
			p.emit(tt.event)
			if tt.approval {
				reply := p.request("")
				if reply.ID != 100 || string(reply.Result["decision"]) != `"decline"` {
					t.Fatalf("approval reply id=%d", reply.ID)
				}
			}
			p.request(methodTurnInterrupt)
			p.emit(tt.event)
			if tt.approval {
				p.request("")
			}
			// A success notification must not erase the earlier maintenance violation.
			p.emit(`{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u1","status":"completed"}}}`)
			r := p.request("thread/read")
			p.reply(r.ID, `{"thread":{"model":"new","reasoningEffort":"low"}}`)
			if result := p.selected(); result.Err == nil {
				t.Fatalf("violation became success: %+v", result)
			}
		})
	}
}

func TestModelSelection_ignoresForeignAndDuplicateCompletions(t *testing.T) {
	p := newModelPeer(t)
	if err := p.c.SelectModel(modelTestOption, ""); err != nil {
		t.Fatal(err)
	}
	p.request(methodTurnStart)
	p.emit(`{"method":"turn/completed","params":{"threadId":"another","turn":{"status":"completed"}}}`)
	if len(p.writer.requests) != 0 {
		t.Fatal("foreign completion started readback")
	}
	p.emit(turnStarted)
	completion := `{"method":"turn/completed","params":{"threadId":"t1","turn":{"id":"u1","status":"completed"}}}`
	p.emit(completion)
	r := p.request("thread/read")
	p.emit(completion)
	if len(p.writer.requests) != 0 {
		t.Fatal("duplicate completion started another readback")
	}
	p.reply(r.ID, `{"thread":{"model":"new"}}`)
	if result := p.selected(); result.Err != nil || result.Settings.Effort != "" {
		t.Fatal(result)
	}
}

func TestModelSelection_processExitDoesNotConfirm(t *testing.T) {
	p := newModelPeer(t)
	if err := p.c.SelectModel(modelTestOption, ""); err != nil {
		t.Fatal(err)
	}
	p.request(methodTurnStart)
	if handleStdoutReadError(p.c.lifecycle.ctx, p.c, io.EOF) {
		t.Fatal("process exit kept reader alive")
	}
	if msg, err := p.c.Read(); err == nil {
		t.Fatalf("process exit reported message=%+v", msg)
	}
}

func TestModelCatalog_RPCFailureIsOrderedAndDropsPartialCatalog(t *testing.T) {
	p := newModelPeer(t)
	if err := p.c.Models(); err != nil {
		t.Fatal(err)
	}
	first := p.request("model/list")
	p.reply(first.ID, `{"data":[{"model":"partial"}],"nextCursor":"next"}`)
	next := p.request("model/list")
	raw, _ := json.Marshal(map[string]any{"id": next.ID, "error": map[string]any{"code": -32600}})
	p.emit(string(raw))
	if result := p.listed(); result.Err == nil || result.Models != nil {
		t.Fatal(result)
	}
	if _, err := p.c.Send("work"); err != nil {
		t.Fatal(err)
	}
}

func TestModelSelection_shutdownDoesNotConfirm(t *testing.T) {
	script := `read -r request
printf '%s\n' '{"id":1,"result":{}}'
read -r request
printf '%s\n' '{"id":2,"result":{"model":"old","reasoningEffort":"low","thread":{"id":"t1"}}}'
cat >/dev/null`
	c := New(t.TempDir(), WithAppServerCommand("sh", "-c", script))
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = c.Stop()
		}
	}()
	if err := c.SelectModel(modelTestOption, "low"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped = true
	for {
		msg, err := c.Read()
		if err != nil {
			break
		}
		if _, ok := msg.Content.(agent.ModelSelected); ok {
			t.Fatal("shutdown fabricated model confirmation")
		}
	}
}

func TestModelSelection_readbackWriteFailureMakesSettingsUnknown(t *testing.T) {
	p := newModelPeer(t)
	if err := p.c.SelectModel(modelTestOption, "low"); err != nil {
		t.Fatal(err)
	}
	p.request(methodTurnStart)
	p.emit(turnStarted)
	p.writer.err = io.ErrClosedPipe
	p.emit(`{"method":"turn/completed","params":{"threadId":"t1","turn":{"status":"completed"}}}`)
	result := p.selected()
	if result.Err == nil || result.Settings != (agent.Settings{Backend: "codex"}) {
		t.Fatal(result)
	}
	p.writer.err = nil
	if _, err := p.c.Send("next"); err != nil {
		t.Fatal(err)
	}
}

func TestModelCatalog_initialWriteFailureDoesNotBlockLaterListings(t *testing.T) {
	p := newModelPeer(t)
	// Keep an earlier listing outstanding when the next call fails admission.
	if err := p.c.Models(); err != nil {
		t.Fatal(err)
	}
	first := p.request("model/list")
	p.writer.err = io.ErrClosedPipe
	if err := p.c.Models(); err == nil {
		t.Fatal("expected initial write failure")
	}
	p.writer.err = nil
	if len(p.c.models.listings) != 1 {
		t.Fatalf("pending listings=%d, want only the earlier request", len(p.c.models.listings))
	}
	p.reply(first.ID, `{"data":[{"model":"first"}]}`)
	assertListedModel(t, p.listed(), "first")
	if err := p.c.Models(); err != nil {
		t.Fatal(err)
	}
	next := p.request("model/list")
	p.reply(next.ID, `{"data":[{"model":"next"}]}`)
	assertListedModel(t, p.listed(), "next")
	if len(p.c.models.listings) != 0 {
		t.Fatal("failed write left a stale listing")
	}
	if len(p.c.rpc.pending) != 0 {
		t.Fatal("failed write left a stale response handler")
	}
}

func TestModelSelection_rejectsUnsupportedServerRequests(t *testing.T) {
	for _, phase := range []string{"confirmation", "readback"} {
		for _, method := range []string{"item/tool/requestUserInput", "account/futureRequest"} {
			t.Run(phase+"/"+method, func(t *testing.T) {
				checkUnsupportedModelRequest(t, phase, method)
			})
		}
	}
}

func TestModelSelection_leavesUnrelatedNotificationsForNormalHandling(t *testing.T) {
	p := newModelPeer(t)
	if err := p.c.SelectModel(modelTestOption, ""); err != nil {
		t.Fatal(err)
	}
	p.request(methodTurnStart)
	msg := rpcEnvelope{Method: "account/updated", Params: json.RawMessage(`{"authMode":"chatgpt"}`)}
	if handled, _ := p.c.handleModelEnvelope(p.c.lifecycle.ctx, msg); handled {
		t.Fatal("model confirmation consumed unrelated notification")
	}
	p.emit(turnStarted)
	p.emit(`{"method":"turn/completed","params":{"threadId":"t1","turn":{"status":"completed"}}}`)
	read := p.request("thread/read")
	if handled, _ := p.c.handleModelEnvelope(p.c.lifecycle.ctx, msg); handled {
		t.Fatal("model readback consumed unrelated notification")
	}
	if p.c.turn.state.kind != turnModelReadback {
		t.Fatalf("readback admission state=%v", p.c.turn.state.kind)
	}
	p.assertBusy()
	p.reply(read.ID, `{"thread":{"model":"new"}}`)
	if result := p.selected(); result.Err != nil {
		t.Fatal(result)
	}
	if p.c.turn.state.kind != turnIdle {
		t.Fatalf("finished admission state=%v", p.c.turn.state.kind)
	}
}

func checkUnsupportedModelRequest(t *testing.T, phase, method string) {
	t.Helper()
	p := newModelPeer(t)
	if err := p.c.SelectModel(modelTestOption, ""); err != nil {
		t.Fatal(err)
	}
	p.request(methodTurnStart)
	p.emit(turnStarted)
	var read modelRequest
	if phase == "readback" {
		p.emit(`{"method":"turn/completed","params":{"threadId":"t1","turn":{"status":"completed"}}}`)
		read = p.request("thread/read")
	}
	raw, _ := json.Marshal(map[string]any{"id": 100, "method": method, "params": map[string]any{"threadId": "t1"}})
	p.emit(string(raw))
	reply := p.request("")
	if reply.ID != 100 || reply.Error == nil || reply.Error.Code != -32601 {
		t.Fatalf("unsupported request reply=%+v", reply)
	}
	wantFailure := phase == "confirmation" && method == "item/tool/requestUserInput"
	if wantFailure {
		p.request(methodTurnInterrupt)
	}
	if phase == "confirmation" {
		p.emit(`{"method":"turn/completed","params":{"threadId":"t1","turn":{"status":"completed"}}}`)
		read = p.request("thread/read")
	}
	p.reply(read.ID, `{"thread":{"model":"new"}}`)
	if result := p.selected(); (result.Err != nil) != wantFailure {
		t.Fatalf("result=%+v, wantFailure=%t", result, wantFailure)
	}
}

func assertListedModel(t *testing.T, result agent.ModelsListed, want string) {
	t.Helper()
	if result.Err != nil || len(result.Models) != 1 || result.Models[0].ID != want {
		t.Fatalf("listing=%+v, want %s", result, want)
	}
}
