// Package std implements command modules using the shared runtime contracts.
package std

import (
	"errors"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// WhoCommand contains no session-specific state. Each Prepare call captures a
// system notice for an independent invocation, leaving publication to the coordinator.
type WhoCommand struct{}

var _ runtime.Command = WhoCommand{}

// Name identifies the command in the catalog.
func (WhoCommand) Name() string { return "who" }

// Usage supplies the command's help example.
func (WhoCommand) Usage() string { return "/who" }

// Description supplies the command's help description.
func (WhoCommand) Description() string { return "list agents" }

// Statement identifies the accepted parsed statement.
func (WhoCommand) Statement() promptlang.Statement { return promptlang.Who{} }

// Prepare captures the current participant listing for one invocation.
func (WhoCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	if _, ok := parsed.Value.(promptlang.Who); !ok {
		return nil, errors.New("who requires a who statement")
	}
	if ctx.Participants == nil {
		return nil, errors.New("who requires participant reads")
	}
	views := ctx.Participants.Participants()
	aliases := make([]string, len(views))
	for index, view := range views {
		aliases[index] = view.Alias
	}
	slices.Sort(aliases)
	text := "[no agents]"
	if len(aliases) > 0 {
		text = "[agents] " + strings.Join(aliases, ", ")
	}
	return &whoInvocation{record: room.Record{Kind: room.KindSystem, Text: text}}, nil
}

type whoInvocation struct {
	record room.Record
}

// Go completes immediately; the supplied callback enqueues its system notice.
func (i *whoInvocation) Go(complete func(runtime.Completion)) error {
	complete(runtime.Completion{Records: []room.Record{i.record}})
	return nil
}
