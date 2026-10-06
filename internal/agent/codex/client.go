// Package codex implements the Agent interface for the Codex CLI app-server.
// Communication uses JSON-RPC 2.0 over stdio (newline-delimited JSON).
// See docs/design/pkg-agent-codex.md for the full design rationale.
package codex

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

// Client implements agent.Agent for the Codex app-server.
// Calls must not be made concurrently, except Stop() which may be called
// from another goroutine to interrupt a blocked Read().
type Client struct {
	// proc holds the OS process and stdio pipes.
	proc *process

	// read holds the output queues required by Read().
	read struct {
		messages    chan readMessage // messages from codex stdout and stderr
		bufMessages chan readMessage // channel to enable buffering of readMessages.
		//
		// Note: bufMessages is currently unbuffered, so readCodexOutWorker can still
		// experience backpressure if messageBufferWorker can't keep up (ultimately
		// bounded by how fast the caller drains Read()).
		//
		// Follow-up improvement: replace bufMessages with an unbounded queue (or a
		// sufficiently large buffered channel), or merge stdout reading + pumping
		// into a single worker so reading codex stdout never blocks on Read().
	}

	// rpc serializes requests written to codex stdin and assigns request IDs.
	rpc struct {
		mu    sync.Mutex
		msgID int
		obs   ProtocolObserver
	}

	// turn tracks the in-flight turn lifecycle within the current thread.
	turn struct {
		mu       sync.Mutex
		threadID string
		state    turnState
	}

	approvals struct {
		listener agent.ApprovalListener
		inbox    chan approvalRequest
		bufInbox chan approvalRequest
	}

	notice struct {
		mu                    sync.Mutex
		state                 noticeState
		kind                  noticeTurnKind
		toolViolationReported bool
		buf                   strings.Builder
	}

	lifecycle struct {
		ctx       context.Context
		cancelFn  context.CancelFunc
		waitGroup sync.WaitGroup
	}
}

type turnStateKind uint8

const (
	turnIdle turnStateKind = iota
	turnInflightUnknownID
	turnInflightKnownID
)

type turnState struct {
	kind   turnStateKind
	turnID string
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithObserver attaches a ProtocolObserver that receives every raw JSON line.
func WithObserver(obs ProtocolObserver) Option {
	return func(c *Client) { c.rpc.obs = obs }
}

// WithApprovalListener attaches an optional listener for mid-turn approval requests.
func WithApprovalListener(l agent.ApprovalListener) Option {
	return func(c *Client) { c.approvals.listener = l }
}

// WithContext sets the parent context for the client's worker lifecycle.
// The client derives a child context from ctx so it can still cancel workers
// independently via Stop(). If not set, context.Background() is used.
func WithContext(ctx context.Context) Option {
	return func(c *Client) {
		c.lifecycle.ctx, c.lifecycle.cancelFn = context.WithCancel(ctx) //nolint:gosec // cancel stored in cancelFn, called in Stop()
	}
}

// WithModel sets the model Codex will use (e.g. "o3-mini"). If not set, Codex
// uses its default model.
func WithModel(model string) Option {
	return func(c *Client) { c.proc.model = model }
}

// WithSystemPrompt configures thread-scoped startup instructions that Codex
// receives during thread/start.
func WithSystemPrompt(prompt string) Option {
	return func(c *Client) { c.proc.systemPrompt = prompt }
}

// WithAskForApprovalPolicy configures Codex's command approval policy.
// See `codex --help` for possible values (e.g. "untrusted", "on-request", "never").
// Use `AskDefault` to omit the flag and let Codex choose.
func WithAskForApprovalPolicy(policy AskForApprovalPolicy) Option {
	return func(c *Client) { c.proc.askForApproval = policy }
}

// WithSandboxMode configures Codex's sandbox policy for executing shell commands.
// See `codex --help` for possible values (e.g. "read-only", "workspace-write", "danger-full-access").
// Use `SandboxDefault` to omit the flag and let Codex choose.
func WithSandboxMode(mode SandboxMode) Option {
	return func(c *Client) { c.proc.sandboxMode = mode }
}

// WithReasoningEffort configures Codex's model_reasoning_effort setting.
// Supported values are model-dependent; use ReasoningDefault to omit the override.
func WithReasoningEffort(effort ReasoningEffort) Option {
	return func(c *Client) { c.proc.reasoningEffort = effort }
}

// WithReasoningSummary configures Codex's model_reasoning_summary setting.
// When set, Codex is also told that the model supports reasoning summaries.
func WithReasoningSummary(summary ReasoningSummary) Option {
	return func(c *Client) { c.proc.reasoningSummary = summary }
}

// WithAppServerCommand overrides the subprocess command used to launch the
// app-server peer. When unset, the client launches Codex via `npx ... app-server`.
func WithAppServerCommand(name string, args ...string) Option {
	return func(c *Client) {
		c.proc.appServerCmd = append([]string{name}, args...)
	}
}

// New returns a Client that will run Codex in the given working directory.
func New(cwd string, opts ...Option) *Client {
	c := &Client{proc: newProc(cwd)}
	c.rpc.obs = noopObserver{}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) initRead() {
	if c.read.messages == nil {
		c.read.messages = make(chan readMessage)
		c.read.bufMessages = make(chan readMessage)
	}
}

func (c *Client) initApprovals() {
	if c.approvals.inbox == nil {
		c.approvals.inbox = make(chan approvalRequest)
		// Buffering here is not for throughput; it's a small amount of slack so the
		// stdout reader is less sensitive to scheduler timing. The unbounded slice
		// buffer lives in approvalBufferWorker.
		c.approvals.bufInbox = make(chan approvalRequest, 32)
	}
}

// Start launches the Codex app-server and completes the initialize and
// thread/start handshakes. It must be called before Send or Read.
func (c *Client) Start() error {
	err := c.proc.start()
	if err != nil {
		return err
	}

	c.initRead()
	c.initApprovals()

	threadID, err := rpcHandshake(c)
	if err != nil {
		close(c.read.messages)
		c.read.messages = nil
		close(c.read.bufMessages)
		c.read.bufMessages = nil
		_ = c.Stop()
		// Stop() waits for the process to exit, so the stderr pipe is fully
		// written and safe to drain without blocking regardless of error type.
		stderrBytes, _ := io.ReadAll(c.proc.codexErr)
		_ = c.proc.codexErr.Close()
		if len(stderrBytes) > 0 {
			return fmt.Errorf("%w\n%s", err, strings.TrimRight(string(stderrBytes), "\r\n"))
		}
		return err
	}
	c.turn.mu.Lock()
	c.turn.threadID = threadID
	c.turn.state = turnState{kind: turnIdle}
	c.turn.mu.Unlock()

	if c.lifecycle.ctx == nil {
		c.lifecycle.ctx, c.lifecycle.cancelFn = context.WithCancel(context.Background()) // #nosec: G118
	}
	c.initWorkers()
	return nil
}

func (c *Client) initWorkers() {
	var workers = []workerFn{
		readCodexErrWorker,
		readCodexOutWorker,
		messageBufferWorker,
		approvalBufferWorker,
		approvalLoopWorker,
	}
	for _, worker := range workers {
		worker := worker
		c.lifecycle.waitGroup.Go(
			func() {
				worker(c.lifecycle.ctx, c)
			})
	}
}

// Interrupt requests the Codex process to stop its current in-flight work.
// If a turn is active, send a turn/interrupt request.
func (c *Client) Interrupt() error {
	c.turn.mu.Lock()
	threadID := c.turn.threadID
	state := c.turn.state
	c.turn.mu.Unlock()

	// if the turn is being established, there is no active work being done
	// and we don't know the turn ID yet, so we can't send turn/interrupt.
	if state.kind == turnInflightUnknownID {
		return nil
	}
	if threadID == "" || state.turnID == "" {
		return nil
	}
	return rpcWrite(c, methodTurnInterrupt, turnInterruptParams{
		ThreadID: threadID,
		TurnID:   state.turnID,
	})
}

// Send writes a turn/start request to stdin and returns immediately.
// It does not read from stdout. Notifications arrive via Read().
func (c *Client) Send(prompt string) (agent.StreamID, error) {
	c.turn.mu.Lock()
	if c.turn.state.kind != turnIdle {
		c.turn.mu.Unlock()
		return "", agent.ErrTurnInProgress
	}
	c.turn.state = turnState{kind: turnInflightUnknownID}
	threadID := c.turn.threadID
	c.turn.mu.Unlock()

	err := rpcWrite(c, methodTurnStart, turnStartParams{
		ThreadID: threadID,
		Input:    []turnInput{{Type: "text", Text: prompt}},
	})
	if err != nil {
		c.turn.mu.Lock()
		c.turn.state = turnState{kind: turnIdle}
		c.turn.mu.Unlock()
		return "", err
	}
	return activeTurnStreamID, nil
}

// SendNotice delivers context to the agent without expecting a substantive
// response. The prompt is wrapped with a CONTEXT UPDATE prefix instructing the
// model to return only {"acknowledge":true}. Any JSON response containing
// "acknowledge":true is silently discarded; other responses surface as reasoning.
func (c *Client) SendNotice(prompt string) (agent.StreamID, error) {
	err := c.startNoticeTurn(noticeContextPrefix+prompt, noticeTurnDelivery)
	if err != nil {
		return "", err
	}
	return noticeTurnStreamID, nil
}

func (c *Client) startNoticeTurn(prompt string, kind noticeTurnKind) error {
	c.turn.mu.Lock()
	if c.turn.threadID == "" {
		c.turn.mu.Unlock()
		return fmt.Errorf("codex: thread not started")
	}
	if c.turn.state.kind != turnIdle {
		c.turn.mu.Unlock()
		return agent.ErrTurnInProgress
	}
	c.turn.state = turnState{kind: turnInflightUnknownID}
	threadID := c.turn.threadID
	c.turn.mu.Unlock()

	c.notice.mu.Lock()
	c.notice.state = noticePending
	c.notice.kind = kind
	c.notice.toolViolationReported = false
	c.notice.buf.Reset()
	c.notice.mu.Unlock()

	err := rpcWrite(c, methodTurnStart, turnStartParams{
		ThreadID:     threadID,
		Input:        []turnInput{{Type: "text", Text: prompt}},
		OutputSchema: noticeAcknowledgementSchema,
	})
	if err != nil {
		c.turn.mu.Lock()
		c.turn.state = turnState{kind: turnIdle}
		c.turn.mu.Unlock()

		c.notice.mu.Lock()
		c.notice.state = noticeIdle
		c.notice.kind = noticeTurnNone
		c.notice.toolViolationReported = false
		c.notice.mu.Unlock()
		return err
	}
	return nil
}

// Read blocks until a meaningful message is ready — either a stdout-derived
// notification (delta, done) or a queued stderr line (log). Both sources are
// waited on simultaneously so neither can stall the other. A closed
// read.messages channel means the process has exited and no further messages
// will arrive.
func (c *Client) Read() (agent.Message, error) {
	if c.read.messages == nil {
		return agent.Message{}, fmt.Errorf("codex: client not started")
	}
	r, ok := <-c.read.messages
	if !ok {
		return agent.Message{}, fmt.Errorf("codex: process exited")
	}
	return r.msg, r.err
}

// KeepAliveSchedule returns how often the session should ping Codex to keep
// the thread warm while it is otherwise idle.
func (c *Client) KeepAliveSchedule() time.Duration {
	return 20 * time.Minute
}

// KeepAlive starts a maintenance notice turn so the model-side thread context
// remains active during otherwise idle periods.
func (c *Client) KeepAlive() error {
	return c.startNoticeTurn(keepaliveNoticePrompt, noticeTurnKeepalive)
}

func (c *Client) updateTurnState(method string, p *turnStartedParams) {
	switch method {
	case methodTurnStarted:
		if p == nil || p.ThreadID == "" || p.Turn.ID == "" {
			return
		}
		c.turn.mu.Lock()
		// Ignore stray turns from other threads.
		if c.turn.threadID == p.ThreadID {
			c.turn.state = turnState{kind: turnInflightKnownID, turnID: p.Turn.ID}
		}
		c.turn.mu.Unlock()
	case methodTurnCompleted, methodTurnFailed:
		c.turn.mu.Lock()
		c.turn.state = turnState{kind: turnIdle}
		c.turn.mu.Unlock()
	}
}

const stopGracePeriod = 5 * time.Second

// Stop closes stdin and waits for the Codex process to exit.
// If the process does not exit within stopGracePeriod it is killed.
// May be called from a different goroutine to interrupt a blocked Read().
func (c *Client) Stop() error {
	if c.lifecycle.cancelFn != nil {
		c.lifecycle.cancelFn()
		c.lifecycle.cancelFn = nil
	}
	if c.proc.codexIn != nil {
		_ = c.proc.codexIn.Close()
	}

	done := make(chan error, 1)
	c.lifecycle.waitGroup.Go(
		func() {
			if c.proc.cmd == nil || c.proc.cmd.Process == nil {
				return
			}
			done <- c.proc.cmd.Wait()
		})

	go func() {
		c.lifecycle.waitGroup.Wait()
		if c.read.messages != nil {
			close(c.read.messages)
		}
		if c.read.bufMessages != nil {
			close(c.read.bufMessages)
		}
	}()

	timer := time.NewTimer(stopGracePeriod)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("codex wait: %w", err)
		}
		return nil
	case <-timer.C:
		_ = c.proc.cmd.Process.Kill()
		<-done
		return nil
	}
}
