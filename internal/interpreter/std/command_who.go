// Package std implements command modules using the shared runtime contracts.
package std

import (
	"errors"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// WhoCommand contains no session-specific state. Each Prepare call captures a
// system notice for an independent invocation, leaving publication to the coordinator.
//
// User documentation: ../../../docs/man/who.md
type WhoCommand struct{}

var _ runtime.Command = WhoCommand{}

// Name identifies the command in the catalog.
func (WhoCommand) Name() string { return "who" }

// Usage supplies the command's help example.
func (WhoCommand) Usage() string { return "/who" }

// Description supplies the command's help description.
func (WhoCommand) Description() string { return "list agents and their details" }

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
	views := slices.Clone(ctx.Participants.Participants())
	slices.SortFunc(views, func(a, b participant.View) int { return strings.Compare(a.Alias.Name, b.Alias.Name) })
	return &whoInvocation{record: room.Record{Kind: room.KindSystem, Text: whoNotice(views), Participants: views}}, nil
}

// whoNotice lists sorted participants by alias, each with its backend when
// known.
func whoNotice(views []participant.View) string {
	if len(views) == 0 {
		return "[no agents]"
	}
	entries := make([]string, len(views))
	for i, view := range views {
		entries[i] = view.Alias.Name
		if view.Settings.Backend != "" {
			entries[i] += " (" + view.Settings.Backend + ")"
		}
	}
	return "[agents] " + strings.Join(entries, ", ")
}

type whoInvocation struct {
	record room.Record
}

func (*whoInvocation) Staging() *runtime.Staging { return nil }

// Go completes immediately; the supplied callback enqueues its system notice.
func (i *whoInvocation) Go(complete func(runtime.CommandResult)) error {
	complete(runtime.CommandResult{Records: []room.Record{i.record}})
	return nil
}

func (*whoInvocation) Close() {}
