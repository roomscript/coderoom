package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Fixtures are meant to be committed. Claude reports account details in
// the initialize response and local paths everywhere, so every recorded
// line goes through redact.

var redactedKeys = map[string]bool{"email": true, "organization": true}

var (
	home     = os.Getenv("HOME")
	userWord = userPattern(os.Getenv("USER"))
)

func userPattern(user string) *regexp.Regexp {
	if user == "" {
		return nil
	}
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(user) + `\b`)
}

// redact returns line with account fields and local identity removed. Lines
// that need no change are returned as is, keeping their original bytes.
func redact(line []byte) []byte {
	var v any
	if err := json.Unmarshal(line, &v); err != nil {
		return []byte(redactString(string(line)))
	}
	v, changed := redactValue(v)
	if !changed {
		return line
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func redactValue(v any) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		for k, inner := range t {
			if _, isString := inner.(string); isString && redactedKeys[k] {
				t[k] = "<redacted>"
				changed = true
				continue
			}
			if nv, c := redactValue(inner); c {
				t[k] = nv
				changed = true
			}
		}
		return t, changed
	case []any:
		changed := false
		for i, inner := range t {
			if nv, c := redactValue(inner); c {
				t[i] = nv
				changed = true
			}
		}
		return t, changed
	case string:
		r := redactString(t)
		return r, r != t
	}
	return v, false
}

func redactString(s string) string {
	if home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	if userWord != nil {
		s = userWord.ReplaceAllString(s, "user")
	}
	return s
}

// redactFixtures rewrites every existing fixture through redact.
func redactFixtures() error {
	paths, err := filepath.Glob(filepath.Join(fixturesDir, "*.jsonl"))
	if err != nil {
		return err
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		var out bytes.Buffer
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			out.Write(redact(sc.Bytes()))
			out.WriteByte('\n')
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
		if err := os.WriteFile(p, out.Bytes(), 0o644); err != nil {
			return err
		}
		logf("redacted %s", p)
	}
	return nil
}
