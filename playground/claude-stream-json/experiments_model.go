package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Experiments for issue #71: inspecting and changing a participant's model
// and effort within one session, through control requests.

const controlTimeout = 15 * time.Second

// controlReply is the outcome of one control request we sent.
type controlReply struct {
	ok       bool
	errText  string
	response map[string]any
}

// call sends a control request and waits for its response. Control requests
// from the CLI that arrive meanwhile are rejected.
func (s *session) call(req map[string]any) (controlReply, error) {
	id, err := s.control(req)
	if err != nil {
		return controlReply{}, err
	}
	m, err := s.until(controlTimeout, func(m msg) {
		if m.Type == "control_request" {
			_ = s.rejectControl(m)
		}
	}, isControlResponse(id))
	if err != nil {
		return controlReply{}, fmt.Errorf("%v: %w", req["subtype"], err)
	}
	r, _ := m.Body["response"].(map[string]any)
	body, _ := r["response"].(map[string]any)
	return controlReply{ok: r["subtype"] == "success", errText: str(r["error"]), response: body}, nil
}

// applied returns get_settings' applied model and effort: the values the
// session runs with.
func (s *session) applied() (map[string]any, error) {
	r, err := s.call(map[string]any{"subtype": "get_settings"})
	if err != nil {
		return nil, err
	}
	a, _ := r.response["applied"].(map[string]any)
	return map[string]any{"model": a["model"], "effort": a["effort"]}, nil
}

// step sends one mutation, records its reply and the readback after it.
func (s *session) step(key string, req map[string]any) error {
	r, err := s.call(req)
	if err != nil {
		return err
	}
	a, err := s.applied()
	if err != nil {
		return err
	}
	s.observe(key, map[string]any{"ok": r.ok, "error": r.errText, "applied": a})
	return nil
}

func setModel(model any) map[string]any {
	return map[string]any{"subtype": "set_model", "model": model}
}

func setEffort(effort any) map[string]any {
	return map[string]any{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": effort}}
}

// E11: catalog, startup settings and every mutation, without a model turn.
// The project configures model and effort, as a user's CLI settings would.
func e11ModelSettings() error {
	dir, err := workdir("e11")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		return err
	}
	settings := `{"model":"sonnet","effortLevel":"low"}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
		return err
	}
	// No --model: the session starts from the CLI-managed settings.
	s, err := start(startOpts{fixture: "e11-model-settings", dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()

	init, err := s.initialize()
	if err != nil {
		return err
	}
	envelope, _ := init.Body["response"].(map[string]any)
	resp, _ := envelope["response"].(map[string]any)
	s.observe("initialize_has_current_model", resp["model"] != nil)
	s.observe("catalog", catalogSummary(resp["models"]))

	a, err := s.applied()
	if err != nil {
		return err
	}
	s.observe("startup_applied", a)

	steps := []struct {
		key string
		req map[string]any
	}{
		{"set_model_alias", setModel("opus")},
		{"set_model_unknown", setModel("sonet")},
		{"set_model_null", setModel(nil)},
		{"set_effort_valid", setEffort("high")},
		{"set_effort_invalid", setEffort("bogus")},
		{"set_model_keeps_effort", setModel("sonnet")},
		{"set_model_without_effort_support", setModel("claude-haiku-4-5-20251001")},
		{"set_model_back_to_effort_support", setModel("sonnet")},
		{"unset_effort", setEffort(nil)},
		{"set_model_default", setModel("default")},
	}
	for _, st := range steps {
		if err := s.step(st.key, st.req); err != nil {
			return err
		}
	}
	return nil
}

// catalogSummary keeps the catalog fields /model needs.
func catalogSummary(v any) []map[string]any {
	models, _ := v.([]any)
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		mm, _ := m.(map[string]any)
		out = append(out, map[string]any{
			"value":                 mm["value"],
			"resolvedModel":         mm["resolvedModel"],
			"supportsEffort":        mm["supportsEffort"],
			"supportedEffortLevels": mm["supportedEffortLevels"],
		})
	}
	return out
}

// E12: the conversation survives set_model and the next turn runs on the
// new model.
func e12ModelSwitch() error {
	dir, err := workdir("e12")
	if err != nil {
		return err
	}
	s, err := start(startOpts{fixture: "e12-model-switch", args: commonArgs(), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}
	if _, err := s.turn("Remember the word pelican. Reply with just: ok", turnTimeout, nil); err != nil {
		return err
	}
	if err := s.step("switch", setModel("sonnet")); err != nil {
		return err
	}
	var turnModel any
	if err := s.user("Which word did I ask you to remember? Reply with just the word."); err != nil {
		return err
	}
	var text string
	res, err := s.until(turnTimeout, func(m msg) {
		if m.Subtype == "init" {
			turnModel = m.Body["model"]
		}
		text += textDelta(m)
	}, func(m msg) bool { return m.Type == "result" })
	if err != nil {
		return err
	}
	s.observe("init_model_after_switch", turnModel)
	s.observe("context_kept", contains(text, "pelican"))
	s.observe("result_model_usage", keys(res.Body["modelUsage"]))
	return nil
}

// E13: the selected effort reaches the Messages API request. Claude talks to
// a local stub that records the request's model and output_config and fails
// the request, so no quota is used. Headers are never recorded.
func e13EffortRequest() error {
	stub, err := startAPIStub()
	if err != nil {
		return err
	}
	defer stub.close()
	dir, err := workdir("e13")
	if err != nil {
		return err
	}
	s, err := start(startOpts{
		fixture: "e13-effort-request", args: []string{"--model", "sonnet"}, dir: dir,
		env: []string{"ANTHROPIC_BASE_URL=" + stub.url},
	})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}
	cases := []struct {
		key    string
		effort any
	}{
		{"startup", nil},
		{"low", "low"},
		{"max", "max"},
		{"invalid_after_max", "bogus"},
		{"unset", nil},
	}
	for i, c := range cases {
		if i > 0 {
			if err := s.step("set_"+c.key, setEffort(c.effort)); err != nil {
				return err
			}
		}
		before := stub.count()
		if _, err := s.turn("Reply with just: ok", turnTimeout, nil); err != nil {
			return err
		}
		s.observe("request_"+c.key, stub.since(before))
	}
	return nil
}

// apiStub records the fields of each Messages request that carry the model
// and effort.
type apiStub struct {
	url  string
	srv  *http.Server
	mu   sync.Mutex
	seen []map[string]any
}

func startAPIStub() (*apiStub, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	st := &apiStub{url: "http://" + ln.Addr().String()}
	st.srv = &http.Server{Handler: http.HandlerFunc(st.serve), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = st.srv.Serve(ln) }()
	return st, nil
}

func (st *apiStub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	if json.Unmarshal(body, &req) == nil && req["model"] != nil {
		st.mu.Lock()
		st.seen = append(st.seen, map[string]any{
			"path": r.URL.Path, "model": req["model"], "output_config": req["output_config"],
		})
		st.mu.Unlock()
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"playground stub"}}`))
}

func (st *apiStub) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.seen)
}

func (st *apiStub) since(n int) []map[string]any {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]map[string]any(nil), st.seen[n:]...)
}

func (st *apiStub) close() { _ = st.srv.Close() }
