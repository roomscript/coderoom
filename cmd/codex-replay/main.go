// Command codex-replay replays one transcript fixture as a JSON-RPC peer over
// stdin/stdout for deterministic Codex adapter tests.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/roomscript/coderoom/internal/transcript"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	path, err := resolveTranscriptPath(os.Args[1:])
	if err != nil {
		return fmt.Errorf("resolve transcript: %w", err)
	}
	file, err := openTranscript(path)
	if err != nil {
		return err
	}
	defer closeTranscript(file)

	_, steps, err := transcript.ReadOutput(file)
	if err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	if err := serveReplay(os.Stdin, os.Stdout, steps); err != nil {
		return fmt.Errorf("replay transcript: %w", err)
	}
	return nil
}

func resolveTranscriptPath(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--transcript" {
		return "", fmt.Errorf("usage: codex-replay --transcript <path>")
	}
	return filepath.Clean(args[1]), nil
}

func openTranscript(path string) (*os.File, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	return file, nil
}

func closeTranscript(file *os.File) {
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close transcript: %v\n", err)
	}
}
