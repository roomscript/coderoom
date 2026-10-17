package std

import (
	"errors"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/api"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// HelpCommand reads the existing catalogue without owning another registry.
type HelpCommand struct {
	// ReadHelp runs on the interpreter lane during preparation.
	ReadHelp func() api.HelpListed
}

var _ runtime.Command = HelpCommand{}

// Name identifies the command in the catalog.
func (HelpCommand) Name() string { return "help" }

// Usage supplies the command's help example.
func (HelpCommand) Usage() string { return "/help" }

// Description supplies the command's help description.
func (HelpCommand) Description() string { return "show this message" }

// Statement identifies the accepted parsed statement.
func (HelpCommand) Statement() promptlang.Statement { return promptlang.Help{} }

// Prepare captures detached catalogue metadata without publishing it.
func (c HelpCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	if _, ok := parsed.Value.(promptlang.Help); !ok {
		return nil, errors.New("help requires a help statement")
	}
	if c.ReadHelp == nil || ctx.Publication == nil {
		return nil, errors.New("help requires catalogue reads and publication")
	}
	listing := c.ReadHelp()
	listing.Commands = slices.Clone(listing.Commands)
	listing.Messages = slices.Clone(listing.Messages)
	return &helpInvocation{listing: listing, publication: ctx.Publication}, nil
}

type helpInvocation struct {
	listing     api.HelpListed
	publication runtime.InvocationPublication
}

func (*helpInvocation) Staging() *runtime.Staging { return nil }
func (*helpInvocation) ReadyToAcknowledge() bool  { return false }
func (i *helpInvocation) Go(complete func(runtime.CommandResult)) error {
	// Preserve the input snapshot before the help event.
	i.publication.Publish(nil)
	i.publication.Publish(nil, i.listing)
	complete(runtime.CommandResult{})
	return nil
}
func (*helpInvocation) Close() {}
