package interpreter

import (
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
)

type catalogueWho struct{ std.WhoCommand }

func (catalogueWho) Usage() string       { return "/who custom" }
func (catalogueWho) Description() string { return "custom registered description" }

func TestHelpCatalogue_readsRegisteredMetadata(t *testing.T) {
	registry := runtime.NewRegistry()
	if err := registry.Register(catalogueWho{}); err != nil {
		t.Fatal(err)
	}
	interp := &Interpreter{commands: registry}
	listing := interp.readHelp()
	if len(listing.Commands) != 1 || listing.Commands[0].Usage != "/who custom" || listing.Commands[0].Description != "custom registered description" {
		t.Fatalf("listing=%#v", listing)
	}
}

func TestHelpCatalogue_returnsDetachedEntries(t *testing.T) {
	interp := &Interpreter{commands: newCommandRegistry(nil)}
	listing := interp.readHelp()
	listing.Commands[0].Usage = "changed"
	next := interp.readHelp()
	if next.Commands[0].Usage != "/policy enable <name>" {
		t.Fatalf("listing=%#v", next)
	}
}

// An extension statement need not appear in a built-in help list.
type catalogueStatement struct{ promptlang.Statement }
type catalogueExtension struct{ catalogueWho }

func (catalogueExtension) Name() string                    { return "extension" }
func (catalogueExtension) Statement() promptlang.Statement { return catalogueStatement{} }
func (catalogueExtension) Usage() string                   { return "/extension" }

func TestHelpCatalogue_includesExtensionsInRegistrationOrder(t *testing.T) {
	registry := runtime.NewRegistry()
	for _, command := range []runtime.Command{catalogueExtension{}, catalogueWho{}, std.BroadcastCommand{}} {
		if err := registry.Register(command); err != nil {
			t.Fatal(err)
		}
	}
	listing := (&Interpreter{commands: registry}).readHelp()
	want := []HelpEntry{
		{Usage: "/extension", Description: "custom registered description"},
		{Usage: "/who custom", Description: "custom registered description"},
		{Usage: "<text>", Description: "broadcast to all agents"},
	}
	if !reflect.DeepEqual(listing.Commands, want) {
		t.Fatalf("commands=%#v", listing.Commands)
	}
}
