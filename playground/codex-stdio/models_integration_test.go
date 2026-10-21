//go:build integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestModelProtocol probes the wire directly, independently of adapter behavior.
// Run with go test -tags integration -run TestModelProtocol -v.
func TestModelProtocol(t *testing.T) {
	model := os.Getenv("CODEX_MODEL_PROBE_MODEL")
	if model == "" {
		t.Skip("set CODEX_MODEL_PROBE_MODEL explicitly; this integration probe consumes quota")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	version := os.Getenv("CODEX_MODEL_PROBE_VERSION")
	if version == "" {
		version = "0.163.0"
	}
	cmd := exec.CommandContext(ctx, "npx", "--yes", "@openai/codex@"+version, "app-server")
	cmd.Dir = t.TempDir()
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	logPath := os.Getenv("CODEX_MODEL_PROBE_LOG")
	if logPath == "" {
		logPath = filepath.Join(t.TempDir(), "models.jsonl")
	}
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	t.Logf("wire evidence: %s", logPath)
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 8<<20)
	id := 0
	send := func(method string, params any) int {
		id++
		raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(log, "%s\n", raw)
		if _, err := fmt.Fprintf(in, "%s\n", raw); err != nil {
			t.Fatal(err)
		}
		return id
	}
	startedTurn := ""
	read := func() rpcMsg {
		for scan.Scan() {
			raw := scan.Bytes()
			fmt.Fprintf(log, "%s\n", raw)
			var msg rpcMsg
			if json.Unmarshal(raw, &msg) == nil {
				if msg.Method == "turn/started" {
					var started struct{ Turn struct{ ID string } }
					if err := json.Unmarshal(msg.Params, &started); err != nil {
						t.Fatal(err)
					}
					startedTurn = started.Turn.ID
				}
				return msg
			}
		}
		t.Fatalf("stdout ended: %v (context: %v)", scan.Err(), ctx.Err())
		return rpcMsg{}
	}
	response := func(want int) rpcMsg {
		for {
			msg := read()
			if msg.ID != nil && *msg.ID == want {
				return msg
			}
		}
	}
	request := func(method string, params any) json.RawMessage {
		msg := response(send(method, params))
		if len(msg.Error) > 0 {
			t.Fatalf("%s: %s", method, msg.Error)
		}
		return msg.Result
	}
	request("initialize", map[string]any{"clientInfo": map[string]string{"name": "coderoom-model-probe", "version": "0.1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	var catalog struct {
		Data []struct {
			Model   string `json:"model"`
			Efforts []struct {
				Effort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
		}
		Next *string `json:"nextCursor"`
	}
	raw := request("model/list", map[string]any{"limit": 1})
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	t.Logf("first catalog page: %s", raw)
	if catalog.Next != nil {
		t.Logf("next catalog page: %s", request("model/list", map[string]any{"limit": 1, "cursor": *catalog.Next}))
	}
	var thread struct {
		Model  string
		Effort *string `json:"reasoningEffort"`
		Thread struct {
			ID   string
			Path string
		}
	}
	raw = request("thread/start", map[string]any{"cwd": cmd.Dir, "approvalPolicy": "never", "sandbox": "read-only"})
	if err := json.Unmarshal(raw, &thread); err != nil {
		t.Fatal(err)
	}
	t.Logf("startup settings: %s", raw)
	run := func(label string, overrides map[string]any, wantStatus string) (status string) {
		defer func() {
			if os.Getenv("CODEX_MODEL_PROBE_EXPECT_FAILURE") != "1" && status != wantStatus {
				t.Errorf("%s: status=%s, want %s", label, status, wantStatus)
			}
		}()
		interrupt := wantStatus == "interrupted"
		t.Log(label)
		prompt := "Do not use tools. Reply only OK."
		if label == "explicit model and effort" {
			prompt = "Remember the token juniper. " + prompt
		}
		if label == "after interruption" {
			prompt = "What token did I ask you to remember earlier? Reply with just that token. Do not use tools."
		}
		params := map[string]any{"threadId": thread.Thread.ID, "input": []map[string]string{{"type": "text", "text": prompt}}}
		for k, v := range overrides {
			params[k] = v
		}
		startedTurn = ""
		msg := response(send("turn/start", params))
		if len(msg.Error) > 0 {
			t.Logf("rejected: %s", msg.Error)
			return "rejected"
		}
		var result struct{ Turn struct{ ID string } }
		if err := json.Unmarshal(msg.Result, &result); err != nil {
			t.Fatal(err)
		}
		// A start response can precede activation; interrupt only after started.
		for startedTurn != result.Turn.ID {
			read()
		}
		interruptID := 0
		if interrupt {
			interruptID = send("turn/interrupt", map[string]any{"threadId": thread.Thread.ID, "turnId": result.Turn.ID})
		}
		listing := send("model/list", map[string]any{"limit": 1})
		var output strings.Builder
		done, listed, interrupted := false, false, !interrupt
		for !done || !listed || !interrupted {
			msg = read()
			if msg.Method == "item/agentMessage/delta" {
				var delta struct{ Delta string }
				if err := json.Unmarshal(msg.Params, &delta); err != nil {
					t.Fatal(err)
				}
				output.WriteString(delta.Delta)
			}
			if msg.ID != nil && *msg.ID == interruptID {
				interrupted = true
				if len(msg.Error) > 0 && string(msg.Error) != "null" {
					t.Errorf("interrupt rejected: %s", msg.Error)
				}
			}
			if msg.ID != nil && *msg.ID == listing {
				listed = true
				if err := validateProbeCatalog(msg); err != nil {
					t.Fatalf("%s: busy catalog: %v", label, err)
				}
			}
			if msg.Method == "turn/completed" {
				done = true
				t.Logf("completion: %s", msg.Params)
				var completion struct{ Turn struct{ Status string } }
				if err := json.Unmarshal(msg.Params, &completion); err != nil {
					t.Fatal(err)
				}
				status = completion.Turn.Status
			}
		}
		if label == "after interruption" && status == "completed" && strings.TrimSpace(output.String()) != "juniper" {
			t.Errorf("conversation memory: got %q, want juniper", output.String())
		}
		return status
	}
	initialStatus := run("explicit model and effort", map[string]any{"model": model, "effort": "low"}, "completed")
	if initialStatus != "completed" && os.Getenv("CODEX_MODEL_PROBE_EXPECT_FAILURE") != "1" {
		t.Fatalf("initial confirmation failed (%s); stopping further live turns", initialStatus)
	}
	run("no overrides: persistence", nil, "completed")
	otherModel := os.Getenv("CODEX_MODEL_PROBE_OTHER_MODEL")
	if otherModel == "" {
		otherModel = model
	}
	run("model only", map[string]any{"model": otherModel}, "completed")
	run("interrupted explicit effort", map[string]any{"model": model, "effort": "medium"}, "interrupted")
	run("after interruption", nil, "completed")
	if os.Getenv("CODEX_MODEL_PROBE_INVALID") == "1" {
		run("invalid effort string", map[string]any{"model": model, "effort": "bogus"}, "completed")
		run("after invalid effort", nil, "completed")
	}
	if os.Getenv("CODEX_MODEL_PROBE_FAILURES") == "1" {
		readSettings := func() string {
			var result struct {
				Thread struct {
					Model  string
					Effort *string `json:"reasoningEffort"`
				}
			}
			raw := request("thread/read", map[string]any{"threadId": thread.Thread.ID})
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.Thread.Effort == nil {
				t.Fatal("thread/read returned no effort")
			}
			settings := result.Thread.Model + "/" + *result.Thread.Effort
			t.Logf("read settings: %s", settings)
			return settings
		}
		before := readSettings()
		// A malformed input must reject the whole request before applying overrides.
		msg := response(send("turn/start", map[string]any{"threadId": thread.Thread.ID, "model": otherModel, "effort": "high", "input": "invalid"}))
		if len(msg.Error) == 0 || string(msg.Error) == "null" {
			t.Fatal("malformed request unexpectedly accepted")
		}
		t.Logf("pre-admission rejection: %s", msg.Error)
		if after := readSettings(); after != before {
			t.Errorf("rejected request changed settings: before=%s after=%s", before, after)
		}
		run("unsupported model", map[string]any{"model": "gpt-5.4-mini", "effort": "low"}, "failed")
		if got := readSettings(); got != "gpt-5.4-mini/low" {
			t.Errorf("failed generation settings=%s", got)
		}
		run("recovery", map[string]any{"model": model, "effort": "low"}, "completed")
	}
	if thread.Thread.Path != "" {
		raw, err := os.ReadFile(thread.Thread.Path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			var line struct {
				Type    string
				Payload json.RawMessage
			}
			if json.Unmarshal(scanner.Bytes(), &line) == nil && line.Type == "turn_context" {
				t.Logf("recorded context: %s", line.Payload)
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// validateProbeCatalog requires a successful response with a catalog array.
func validateProbeCatalog(msg rpcMsg) error {
	if len(bytes.TrimSpace(msg.Error)) > 0 && !bytes.Equal(bytes.TrimSpace(msg.Error), []byte("null")) {
		return fmt.Errorf("model/list rejected: %s", msg.Error)
	}
	var catalog struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(msg.Result, &catalog); err != nil {
		return fmt.Errorf("decode model/list: %w", err)
	}
	if catalog.Data == nil {
		return fmt.Errorf("model/list returned no catalog array")
	}
	return nil
}

func TestValidateProbeCatalog(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		rpcError string
		wantErr  bool
	}{
		{"catalog", `{"data":[{"model":"gpt-6-sol"}]}`, "", false},
		{"empty catalog", `{"data":[]}`, "", false},
		{"null error", `{"data":[]}`, " null ", false},
		{"RPC rejection", `{"data":[]}`, `{"code":-32600,"message":"busy"}`, true},
		{"missing result", "", "", true},
		{"malformed result", `{`, "", true},
		{"missing catalog", `{}`, "", true},
		{"null catalog", `{"data":null}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := rpcMsg{Result: json.RawMessage(tt.result), Error: json.RawMessage(tt.rpcError)}
			if err := validateProbeCatalog(msg); (err != nil) != tt.wantErr {
				t.Errorf("validateProbeCatalog error=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
