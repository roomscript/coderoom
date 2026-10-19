// Package claude implements the Agent interface for Claude Code, driving one
// long-running claude process over the bidirectional stream-json protocol.
// See docs/design/pkg-agent-claude.md for the full design rationale.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

const (
	// turnStreamID is the anchor returned by Send and flushed on result.
	turnStreamID = agent.StreamID("claude:turn")
	logStreamID  = agent.StreamID("claude:log")

	initRequestID = "coderoom-init"

	// slashEscape goes before a prompt that starts with "/". Claude Code
	// runs such a prompt as one of its own commands instead of passing it
	// to the model: /clear would reset the participant's context. See
	// TestClientLiveSlashPrompt and docs/design/pkg-agent-claude.md.
	slashEscape = `\`

	defaultStartTimeout = 60 * time.Second
	stopGracePeriod     = 5 * time.Second
	stderrTailLines     = 20
)

// turnOwner says whose turn Claude is running. A turn is ours once Claude
// replays the prompt Send or SendNotice wrote; output before that replay
// belongs to a turn Claude started on its own.
type turnOwner int

const (
	ownerNone    turnOwner = iota // no turn running
	ownerUnknown                  // started, owner not known yet
	ownerPrompt                   // answering the prompt from Send
	ownerNotice                   // answering the notice from SendNotice
	ownerSelf                     // started by Claude on its own
)

// pendingPrompt is a prompt written to Claude and not replayed yet.
type pendingPrompt struct {
	notice   bool // written by SendNotice
	outlived bool // a turn ended without answering it
}

// Client implements agent.Agent for Claude Code. Calls must not be made
// concurrently, except Stop() which may be called from another goroutine to
// interrupt a blocked Read(). Stop() must not run concurrently with Start();
// to abort a Start() in progress, cancel the WithContext parent.
type Client struct {
	proc *process
	obs  ProtocolObserver

	startTimeout time.Duration
	stopGrace    time.Duration
	queue        *queue[agent.Message]
	dec          *decoder // stdout reader goroutine only

	approvals struct {
		listener agent.ApprovalListener
		queue    *queue[permissionRequest]
		done     chan struct{} // closed when the approval worker exits
		denied   *deniedTools
		turn     approvalTurn // stdout reader goroutine only
	}

	writeMu sync.Mutex
	reqSeq  int // guarded by writeMu

	turn struct {
		mu      sync.Mutex
		owner   turnOwner
		pending *pendingPrompt // nil when no prompt waits for its replay
	}

	// notice filters the reply of a notice turn. Stdout reader goroutine
	// only.
	notice noticeFilter

	// selfTurnText collects the output of a self-started turn for its
	// summary. Stdout reader goroutine only.
	selfTurnText strings.Builder

	stderrTail struct {
		mu    sync.Mutex
		lines []string
	}

	initDone chan error
	exited   chan struct{}

	lifecycle struct {
		parent   context.Context // ends when the caller ends the lifecycle
		ctx      context.Context
		cancelFn context.CancelFunc
		readers  sync.WaitGroup
		stopOnce sync.Once
	}
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithObserver attaches a ProtocolObserver that receives every raw JSON line.
func WithObserver(obs ProtocolObserver) Option {
	return func(c *Client) { c.obs = obs }
}

// WithContext sets the parent context for the client's worker lifecycle.
// Cancelling it stops the process, as Stop does.
func WithContext(ctx context.Context) Option {
	return func(c *Client) {
		c.lifecycle.parent = ctx
		c.lifecycle.ctx, c.lifecycle.cancelFn = context.WithCancel(ctx) //nolint:gosec // cancel stored in cancelFn, called on process exit and in Stop()
	}
}

// WithApprovalListener attaches a listener for permission requests. Without
// one, every request is declined.
func WithApprovalListener(l agent.ApprovalListener) Option {
	return func(c *Client) { c.approvals.listener = l }
}

// WithModel sets the model passed to --model. If not set, Claude Code uses
// its configured default.
func WithModel(model string) Option {
	return func(c *Client) { c.proc.model = model }
}

// WithSystemPrompt appends participant instructions to Claude Code's own
// system prompt (--append-system-prompt).
func WithSystemPrompt(prompt string) Option {
	return func(c *Client) { c.proc.systemPrompt = prompt }
}

// WithCommand overrides the subprocess command, ignoring the generated claude
// arguments. Used to run fake peers in tests.
func WithCommand(name string, args ...string) Option {
	return func(c *Client) { c.proc.command = append([]string{name}, args...) }
}

// New returns a Client that will run Claude Code in the given working
// directory.
func New(cwd string, opts ...Option) *Client {
	c := &Client{proc: newProc(cwd), obs: noopObserver{}, startTimeout: defaultStartTimeout, stopGrace: stopGracePeriod}
	for _, o := range opts {
		o(c)
	}
	if c.lifecycle.ctx == nil {
		c.lifecycle.parent = context.Background()
		c.lifecycle.ctx, c.lifecycle.cancelFn = context.WithCancel(context.Background()) // #nosec G118 -- cancel called on process exit and in Stop()
	}
	return c
}

// Name returns "claude".
func (c *Client) Name() string { return "claude" }

// Start launches the process and completes the initialize handshake.
func (c *Client) Start() error {
	if err := c.proc.start(); err != nil {
		return err
	}
	c.queue = newQueue[agent.Message]()
	c.dec = newDecoder()
	c.approvals.queue = newQueue[permissionRequest]()
	c.approvals.done = make(chan struct{})
	c.approvals.denied = newDeniedTools()
	c.dec.declined = c.approvals.denied.take
	c.newApprovalTurn()
	c.initDone = make(chan error, 1)
	c.exited = make(chan struct{})
	c.startWorkers()

	if err := c.initialize(); err != nil {
		_ = c.Stop()
		return c.withStderrTail(fmt.Errorf("claude initialize: %w", err))
	}
	return nil
}

func (c *Client) initialize() error {
	err := c.writeJSON(controlRequest{
		Type:      typeControlRequest,
		RequestID: initRequestID,
		Request:   map[string]any{"subtype": controlInitialize, "hooks": nil},
	})
	if err != nil {
		return err
	}
	timer := time.NewTimer(c.startTimeout)
	defer timer.Stop()
	select {
	case err := <-c.initDone:
		return err
	case <-c.exited:
		return errors.New("process exited during handshake")
	case <-timer.C:
		return fmt.Errorf("no initialize response after %s", c.startTimeout)
	}
}

// Send writes a prompt and returns the turn anchor immediately. A leading
// "/" is escaped with slashEscape.
func (c *Client) Send(prompt string) (agent.StreamID, error) {
	if strings.HasPrefix(prompt, "/") {
		prompt = slashEscape + prompt
	}
	if err := c.writePrompt(prompt, false); err != nil {
		return "", err
	}
	return turnStreamID, nil
}

// SendNotice delivers context and asks for a bare acknowledgement, which is
// kept out of Read(). A reply that is not one surfaces as reasoning. The
// instruction is prompt-only: Claude has no per-turn output schema.
func (c *Client) SendNotice(prompt string) (agent.StreamID, error) {
	if err := c.writePrompt(noticeContextPrefix+prompt, true); err != nil {
		return "", err
	}
	return noticeTurnStreamID, nil
}

func (c *Client) writePrompt(prompt string, notice bool) error {
	if err := c.admitPrompt(notice); err != nil {
		return err
	}
	if err := c.writeJSON(newUserMessage(prompt)); err != nil {
		c.dropPrompt()
		return err
	}
	return nil
}

// Read blocks until the next message. It returns an error once the process
// has exited and all queued messages have been read.
func (c *Client) Read() (agent.Message, error) {
	if c.queue == nil {
		return agent.Message{}, errors.New("claude: client not started")
	}
	msg, ok := c.queue.pop()
	if !ok {
		return agent.Message{}, errors.New("claude: process exited")
	}
	return msg, nil
}

// Interrupt asks Claude to stop the turn in flight. The turn then ends with a
// result like any other. It is a no-op when no turn is in flight. A prompt
// that outlived a turn without being answered is released, so Interrupt
// frees the client even when Claude never answers the prompt.
func (c *Client) Interrupt() error {
	if c.queue == nil || c.isIdle() {
		return nil
	}
	err := c.writeJSON(controlRequest{
		Type:      typeControlRequest,
		RequestID: c.nextRequestID("interrupt"),
		Request:   map[string]any{"subtype": controlInterrupt},
	})
	c.releaseOutlivedPrompt()
	return err
}

// releaseOutlivedPrompt ends the pending prompt's turn when no turn is
// running and the prompt already outlived one. A prompt that has seen no
// turn end may still be about to start, so it is left to the interrupt
// request. If Claude answers a released prompt after all, the answer has no
// pending prompt and is summarised as a self-started turn.
func (c *Client) releaseOutlivedPrompt() {
	c.turn.mu.Lock()
	pending := c.turn.pending
	if c.turn.owner != ownerNone || pending == nil || !pending.outlived {
		c.turn.mu.Unlock()
		return
	}
	c.turn.pending = nil
	c.turn.mu.Unlock()

	anchor := turnStreamID
	if pending.notice {
		anchor = noticeTurnStreamID
	}
	c.queue.push(logMessage("claude: prompt released, no turn answered it"),
		agent.Message{StreamID: anchor, Mode: agent.ModeFlush, Content: agent.Output{}})
}

// Stop closes stdin and waits for the process to exit, escalating to SIGTERM
// and then SIGKILL if it does not. A pending approval is abandoned: the
// listener's context is cancelled. May be called from a different goroutine
// to interrupt a blocked Read(), but not during Start().
func (c *Client) Stop() error {
	if c.proc.cmd == nil {
		return nil
	}
	c.lifecycle.stopOnce.Do(func() {
		c.lifecycle.cancelFn()
		_ = c.proc.stdin.Close()
		if c.waitExited(c.stopGrace) {
			return
		}
		_ = c.proc.cmd.Process.Signal(syscall.SIGTERM)
		if c.waitExited(c.stopGrace) {
			return
		}
		_ = c.proc.cmd.Process.Kill()
		// A child that inherited the pipes can outlive the kill and keep
		// them open. Closing our ends makes the readers return.
		_ = c.proc.stdout.Close()
		_ = c.proc.stderr.Close()
		<-c.exited
	})
	<-c.approvals.done
	return nil
}

func (c *Client) waitExited(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-c.exited:
		return true
	case <-timer.C:
		return false
	}
}

// admitPrompt records the prompt about to be written. Only one prompt
// can be waiting or running, and none while Claude runs a turn of its own.
func (c *Client) admitPrompt(notice bool) error {
	if c.queue == nil {
		return errors.New("claude: client not started")
	}
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.owner != ownerNone || c.turn.pending != nil {
		return agent.ErrTurnInProgress
	}
	c.turn.pending = &pendingPrompt{notice: notice}
	return nil
}

func (c *Client) dropPrompt() {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	c.turn.pending = nil
}

func (c *Client) currentOwner() turnOwner {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	return c.turn.owner
}

func (c *Client) isIdle() bool {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	return c.turn.owner == ownerNone && c.turn.pending == nil
}

func (c *Client) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("claude encode: %w", err)
	}
	// The observer runs outside the lock, so the order of logged lines
	// between concurrent writers is best effort.
	c.obs.OnSend(string(b))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.proc.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("claude write: %w", err)
	}
	return nil
}

func (c *Client) nextRequestID(prefix string) string {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.reqSeq++
	return fmt.Sprintf("coderoom-%s-%d", prefix, c.reqSeq)
}

func (c *Client) withStderrTail(err error) error {
	c.stderrTail.mu.Lock()
	defer c.stderrTail.mu.Unlock()
	if len(c.stderrTail.lines) == 0 {
		return err
	}
	return fmt.Errorf("%w; stderr: %s", err, strings.Join(c.stderrTail.lines, "\n"))
}
