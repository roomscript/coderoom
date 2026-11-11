package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roomscript/coderoom/internal/transcript"
)

func redactRecording(output transcript.Output, steps []transcript.Step, home string) (transcript.Output, []transcript.Step, error) {
	// A JSON round trip also decodes the observer's json.RawMessage values.
	type recording struct {
		Expect transcript.Expect
		Steps  []transcript.Step
	}
	raw, err := json.Marshal(recording{output.Expect, steps})
	if err != nil {
		return output, nil, fmt.Errorf("encode recording for redaction: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return output, nil, fmt.Errorf("decode recording for redaction: %w", err)
	}
	raw, err = json.Marshal(redactValue(value, home))
	if err != nil {
		return output, nil, fmt.Errorf("encode redacted recording: %w", err)
	}
	var redacted recording
	if err := json.Unmarshal(raw, &redacted); err != nil {
		return output, nil, fmt.Errorf("decode redacted recording: %w", err)
	}
	output.Expect = redacted.Expect
	return output, redacted.Steps, nil
}

func redactValue(value any, home string) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if _, ok := item.(string); ok && (key == "email" || key == "organization") {
				value[key] = "<redacted>"
				continue
			}
			value[key] = redactValue(item, home)
		}
	case []any:
		for index, item := range value {
			value[index] = redactValue(item, home)
		}
	case string:
		return redactHomePath(value, home)
	}
	return value
}

func redactHomePath(value, home string) string {
	if home == "" {
		return value
	}
	if value == home {
		return "~"
	}
	return strings.ReplaceAll(value, home+"/", "~/")
}
