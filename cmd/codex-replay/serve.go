package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/roomscript/coderoom/internal/transcript"
)

func serveReplay(stdin io.Reader, stdout io.Writer, steps []transcript.Step) error {
	scanner := bufio.NewScanner(stdin)
	enc := json.NewEncoder(stdout)

	for index, step := range steps {
		switch step.Kind {
		case "recv":
			if err := serveRecvStep(scanner, step); err != nil {
				return fmt.Errorf("step %d recv: %w", index, err)
			}
		case "send":
			if err := serveSendStep(enc, step); err != nil {
				return fmt.Errorf("step %d send: %w", index, err)
			}
		default:
			return fmt.Errorf("step %d: unknown kind %q", index, step.Kind)
		}
	}

	if scanner.Scan() {
		return fmt.Errorf("unexpected extra input: %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan replay stdin: %w", err)
	}
	return nil
}

func serveRecvStep(scanner *bufio.Scanner, step transcript.Step) error {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan stdin: %w", err)
		}
		return io.EOF
	}
	if err := transcript.MatchReplayLine(step.Match, scanner.Bytes()); err != nil {
		return fmt.Errorf("match replay input: %w", err)
	}
	return nil
}

func serveSendStep(enc *json.Encoder, step transcript.Step) error {
	if step.DelayMS > 0 {
		time.Sleep(time.Duration(step.DelayMS) * time.Millisecond)
	}
	if err := enc.Encode(step.Message); err != nil {
		return fmt.Errorf("encode replay output: %w", err)
	}
	return nil
}
