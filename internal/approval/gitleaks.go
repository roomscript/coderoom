package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const scannerConfig = "[extend]\nuseDefault = true\n"
const scannerConfigID = "gitleaks-default-v2-decode3-no-inline-allow"

// Gitleaks runs the installed scanner in an isolated temporary directory.
type Gitleaks struct {
	root string
	// run is a narrow process seam for deterministic failure tests.
	run func(context.Context, string, string, []byte, ...string) ([]byte, error)
}

// Discover reports whether a local executable is installed.
func (Gitleaks) Discover() bool { _, err := exec.LookPath("gitleaks"); return err == nil }

// Scan validates local bytes with the installed scanner.
func (g Gitleaks) Scan(ctx context.Context, input []byte) Result {
	path, err := exec.LookPath("gitleaks")
	if err != nil {
		return decline("get.scanner_error")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return decline("get.scanner_error")
	}
	if !scannerPathSafe(path, g.root) {
		return decline("get.scanner_error")
	}
	return g.scan(ctx, path, input)
}

var scannerVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

func (g Gitleaks) scan(ctx context.Context, path string, input []byte) (result Result) {
	result = decline("get.scanner_error")
	// Legacy releases honor inline suppression without an off switch. Refuse
	// directives before scanning rather than trusting a suppressed clean report.
	if bytes.Contains(bytes.ToLower(input), []byte("gitleaks:allow")) {
		return decline("get.validation_incomplete")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			result = decline("get.scanner_timeout")
		}
	}()
	dir, err := os.MkdirTemp("", "coderoom-gitleaks-")
	if err != nil {
		return result
	}
	defer os.RemoveAll(dir) //nolint:errcheck // best-effort cleanup of application-owned scratch directory
	if err := os.WriteFile(filepath.Join(dir, "rules.toml"), []byte(scannerConfig), 0600); err != nil {
		return result
	}
	run := g.run
	if run == nil {
		run = runScanner
	}
	profile, ok := discoverScannerProfile(ctx, run, path, dir)
	if !ok {
		return result
	}
	result.ScannerVersion = readScannerVersion(ctx, run, path, dir)
	result.ScannerConfig = scannerConfigID + "/" + profile.name
	return runGitleaksScan(ctx, run, path, dir, input, result, profile)
}

type scannerRunner func(context.Context, string, string, []byte, ...string) ([]byte, error)

type scannerProfile struct {
	name         string
	command      []string
	ignoreAllow  bool
	nativeDecode bool
}

func discoverScannerProfile(ctx context.Context, run scannerRunner, path, dir string) (scannerProfile, bool) {
	profiles := []scannerProfile{
		{name: "stdin", command: []string{"stdin"}},
		{name: "detect-pipe", command: []string{"detect", "--pipe"}},
	}
	for _, profile := range profiles {
		help, err := run(ctx, path, dir, nil, profile.command[0], "--help")
		if err != nil {
			continue
		}
		if !scannerHelpSupported(string(help), profile) {
			continue
		}
		profile.ignoreAllow = helpHasFlag(string(help), "--ignore-gitleaks-allow")
		profile.nativeDecode = profile.ignoreAllow && helpHasFlag(string(help), "--max-decode-depth")
		return profile, true
	}
	return scannerProfile{}, false
}

func scannerHelpSupported(help string, profile scannerProfile) bool {
	if !strings.Contains(help, "gitleaks "+profile.command[0]) {
		return false
	}
	required := []string{"--config", "--report-format", "--report-path", "--redact", "--exit-code", "--no-banner"}
	if profile.name == "detect-pipe" {
		required = append(required, "--pipe")
	}
	for _, flag := range required {
		if !helpHasFlag(help, flag) {
			return false
		}
	}
	return true
}

func helpHasFlag(help, flag string) bool {
	for _, word := range strings.Fields(help) {
		name, _, _ := strings.Cut(word, "=")
		if name == flag {
			return true
		}
	}
	return false
}

func readScannerVersion(ctx context.Context, run scannerRunner, path, dir string) string {
	version, err := run(ctx, path, dir, nil, "version")
	value := strings.TrimSpace(string(version))
	if err != nil || !scannerVersion.MatchString(value) {
		return "unavailable"
	}
	return value
}

func (p scannerProfile) scanArgs(dir string) []string {
	args := append([]string(nil), p.command...)
	args = append(args, "--config="+filepath.Join(dir, "rules.toml"), "--report-format=json", "--report-path="+filepath.Join(dir, "report.json"), "--redact", "--exit-code=42", "--no-banner")
	if p.ignoreAllow {
		args = append(args, "--ignore-gitleaks-allow")
	}
	if p.nativeDecode {
		args = append(args, "--max-decode-depth=3")
	}
	return args
}

func runGitleaksScan(ctx context.Context, run scannerRunner, path, dir string, input []byte, result Result, profile scannerProfile) Result {
	_, err := run(ctx, path, dir, input, profile.scanArgs(dir)...)
	var exit *exec.ExitError
	findingsExit := errors.As(err, &exit) && exit.ExitCode() == 42
	if err != nil && !findingsExit {
		return result
	}
	findings, ok := readScannerReport(dir)
	if !ok {
		return result
	}
	if len(findings) > 0 {
		result.Outcome = Decline
		result.Reason = "get.secret_detected"
		return result
	}
	if findingsExit {
		return result
	}
	result.Outcome = Approve
	result.Reason = "get.scan_clean"
	return result
}

func readScannerReport(dir string) ([]json.RawMessage, bool) {
	file, err := os.Open(filepath.Join(dir, "report.json")) //nolint:gosec // application-owned private scanner directory
	if err != nil {
		return nil, false
	}
	defer file.Close() //nolint:errcheck // read-only scanner report
	data, err := io.ReadAll(io.LimitReader(file, maxCommandBytes+1))
	if err != nil || len(data) > maxCommandBytes {
		return nil, false
	}
	var findings []json.RawMessage
	if json.Unmarshal(data, &findings) != nil || strings.TrimSpace(string(data)) == "null" {
		return nil, false
	}
	return findings, true
}

// Kill the scanner process group on cancellation and always reap the direct
// child. Candidate bytes travel only on stdin; diagnostics are bounded/discarded.
func runScanner(ctx context.Context, path, dir string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir}
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	output := &limitedOutput{limit: 16 * 1024}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return output.data, fmt.Errorf("scanner process: %w", err)
	}
	if output.truncated {
		return nil, fmt.Errorf("scanner output limit exceeded")
	}
	return output.data, nil
}

type limitedOutput struct {
	data      []byte
	truncated bool
	limit     int
}

func (b *limitedOutput) Write(data []byte) (int, error) {
	n := len(data)
	limit := b.limit
	if limit == 0 {
		limit = 4096
	}
	if len(b.data)+n > limit {
		b.truncated = true
	}
	remaining := limit - len(b.data)
	if remaining > 0 {
		b.data = append(b.data, data[:min(remaining, n)]...)
	}
	return n, nil
}

func scannerPathSafe(path, root string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if root == "" {
		return true
	}
	project, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	return !within(project, resolved)
}
