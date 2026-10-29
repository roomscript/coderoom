package transcript

import (
	"encoding/json"
	"fmt"
)

// MatchReplayLine checks a JSON line against a semantic subset match.
func MatchReplayLine(expected any, raw []byte) error {
	var actual any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("parse input json: %w", err)
	}
	if err := matchReplayValue(expected, actual); err != nil {
		return fmt.Errorf("message mismatch: %w", err)
	}
	return nil
}

func matchReplayValue(expected, actual any) error {
	switch want := expected.(type) {
	case map[string]any:
		return matchReplayObject(want, actual)
	case []any:
		return matchReplayArray(want, actual)
	default:
		return matchReplayScalar(want, actual)
	}
}

func matchReplayObject(expected map[string]any, actual any) error {
	got, ok := actual.(map[string]any)
	if !ok {
		return fmt.Errorf("want object, got %T", actual)
	}
	for key, wantValue := range expected {
		gotValue, ok := got[key]
		if !ok {
			return fmt.Errorf("missing key %q", key)
		}
		if err := matchReplayValue(wantValue, gotValue); err != nil {
			return fmt.Errorf("%s", key+": "+err.Error())
		}
	}
	return nil
}

func matchReplayArray(expected []any, actual any) error {
	got, ok := actual.([]any)
	if !ok {
		return fmt.Errorf("want array, got %T", actual)
	}
	if len(got) != len(expected) {
		return fmt.Errorf("want array length %d, got %d", len(expected), len(got))
	}
	for index, wantValue := range expected {
		if err := matchReplayValue(wantValue, got[index]); err != nil {
			return fmt.Errorf("[%d]: %w", index, err)
		}
	}
	return nil
}

func matchReplayScalar(expected, actual any) error {
	if !valuesEqual(expected, actual) {
		return fmt.Errorf("want %v, got %v", expected, actual)
	}
	return nil
}

func valuesEqual(expected, actual any) bool {
	switch want := expected.(type) {
	case nil:
		return actual == nil
	case string:
		got, ok := actual.(string)
		return ok && got == want
	case bool:
		got, ok := actual.(bool)
		return ok && got == want
	case float64:
		got, ok := actual.(float64)
		return ok && got == want
	default:
		return expected == actual
	}
}
