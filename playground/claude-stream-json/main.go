// Command claude-stream-json probes Claude Code's bidirectional stream-json
// protocol (the one the Agent SDK uses) from a long-running process.
//
// Each experiment records raw stdin/stdout/stderr lines to fixtures/.
// Run one experiment with -exp e3, or all of them with -exp all.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

const fixturesDir = "fixtures"

var (
	model    = flag.String("model", "haiku", "model passed to --model")
	safeMode = flag.Bool("safe-mode", false, "pass --safe-mode (personal config disabled)")
)

type experiment struct {
	name string
	run  func() error
}

var experiments = map[string]experiment{
	"e1":  {"idle start and init handshake", e1Idle},
	"e2":  {"two turns in one process", e2TwoTurns},
	"e3":  {"tool permission allow and deny", e3Permissions},
	"e4":  {"interrupt a long turn", e4Interrupt},
	"e5":  {"partial messages: text, reasoning, tools", e5Partials},
	"e6":  {"Bash cwd from a non-shell parent", e6Cwd},
	"e7":  {"structured output with --json-schema", e7JSONSchema},
	"e8":  {"stop and resume", e8StopResume},
	"e9":  {"--safe-mode versus personal config", e9SafeMode},
	"e10": {"tools that ask, act outside a turn, or reach outside the room", e10Tools},
	"e11": {"model and effort settings through control requests", e11ModelSettings},
	"e12": {"conversation across a model switch", e12ModelSwitch},
	"e13": {"selected effort in the API request", e13EffortRequest},
}

func main() {
	exp := flag.String("exp", "", "experiment to run (e1..e13 or all)")
	redactOnly := flag.Bool("redact-fixtures", false, "rewrite existing fixtures through the redaction filter and exit")
	flag.Parse()

	if *redactOnly {
		if err := redactFixtures(); err != nil {
			fmt.Fprintf(os.Stderr, "redact: %v\n", err)
			os.Exit(1)
		}
		return
	}

	names := []string{*exp}
	if *exp == "all" {
		names = names[:0]
		for n := range experiments {
			names = append(names, n)
		}
		sort.Strings(names)
	}

	failed := false
	for _, n := range names {
		e, ok := experiments[n]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown experiment %q; use one of e1..e13 or all\n", n)
			os.Exit(2)
		}
		logf("\n=== %s: %s ===", n, e.name)
		if err := e.run(); err != nil {
			logf("FAIL %s: %v", n, err)
			failed = true
			continue
		}
		logf("DONE %s", n)
	}
	if failed {
		os.Exit(1)
	}
}

// commonArgs returns the per-run flags shared by all experiments.
func commonArgs(extra ...string) []string {
	args := []string{"--model", *model}
	if *safeMode {
		args = append(args, "--safe-mode")
	}
	return append(args, extra...)
}

// workdir creates a fresh project directory for one experiment.
func workdir(name string) (string, error) {
	return os.MkdirTemp("", "claude-stream-json-"+name+"-")
}

func contains(s, sub string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(sub)) }
