package approval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScannerFailuresAndReports(t *testing.T) {
	for _, tt := range []struct {
		name, report string
		fail         bool
		reason       string
	}{
		{"clean", "[]", false, "get.scan_clean"},
		{"finding", `[{"Secret":"PRIVATE"}]`, false, "get.secret_detected"},
		{"malformed", "PRIVATE", false, "get.scanner_error"},
		{"null", "null", false, "get.scanner_error"},
		{"launch", "", true, "get.scanner_error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var scratch string
			scanner := Gitleaks{run: func(_ context.Context, _ string, dir string, input []byte, args ...string) ([]byte, error) {
				scratch = dir
				if tt.fail {
					return nil, errors.New("PRIVATE")
				}
				if len(args) == 2 && args[1] == "--help" {
					return []byte(modernScannerHelp), nil
				}
				if args[0] == "version" {
					return []byte("8.24.2"), nil
				}
				if string(input) != "PRIVATE" {
					t.Fatal("input not passed")
				}
				assertScannerConfiguration(t, dir)
				return nil, os.WriteFile(filepath.Join(dir, "report.json"), []byte(tt.report), 0600)
			}}
			got := scanner.scan(context.Background(), "/fake/scanner", []byte("PRIVATE"))
			if got.Reason != tt.reason || strings.Contains(got.Reason+got.ScannerConfig+got.ScannerVersion, "PRIVATE") {
				t.Fatalf("got %+v", got)
			}
			if _, err := os.Stat(scratch); !os.IsNotExist(err) {
				t.Fatal("scratch retained")
			}
		})
	}
}

func TestScannerActualInvocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitleaks")
	// This trusted fake scanner is the only executed input; it makes no requests.
	script := `#!/bin/sh
if [ "$2" = --help ]; then echo "` + modernScannerHelp + `"; exit; fi
if [ "$1" = version ]; then echo 8.24.2; exit; fi
for arg do
 case "$arg" in --report-path=*) report=${arg#--report-path=};; esac
done
input=$(/bin/cat)
if [ "$input" = PRIVATE ] && [ -z "$GITLEAKS_CONFIG" ]; then
 echo '[]' > "$report"
else
 exit 3
fi
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil { //nolint:gosec // trusted executable fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GITLEAKS_CONFIG", "PRIVATE")
	got := (Gitleaks{}).Scan(context.Background(), []byte("PRIVATE"))
	if got.Outcome != Approve || got.Reason != "get.scan_clean" || got.ScannerConfig == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestScannerTimeoutAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	scanner := Gitleaks{run: func(ctx context.Context, _ string, _ string, _ []byte, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	if got := scanner.scan(ctx, "/fake", nil); got != decline("get.scanner_timeout") {
		t.Fatalf("got %+v", got)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runScanner(ctx, "/bin/sh", t.TempDir(), nil, "-c", "sleep 10 & wait"); err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("child not stopped promptly")
	}
}

func TestScannerProcessExitSemantics(t *testing.T) {
	for _, tt := range []struct{ name, version, report, exit, reason string }{
		{"finding", "8.24.2", `[{"Secret":"PRIVATE"}]`, "42", "get.secret_detected"},
		{"empty finding exit", "8.24.2", `[]`, "42", "get.scanner_error"},
		{"runtime error", "8.24.2", `[]`, "3", "get.scanner_error"},
		{"unformatted version", "version is set by build process", `[]`, "0", "get.scan_clean"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "gitleaks")
			script := "#!/bin/sh\nif [ \"$2\" = --help ]; then echo '" + modernScannerHelp + "'; exit; fi\nif [ \"$1\" = version ]; then echo " + tt.version + "; exit; fi\nfor arg do case \"$arg\" in --report-path=*) report=${arg#--report-path=};; esac; done\necho '" + tt.report + "' > \"$report\"\nexit " + tt.exit + "\n"
			if err := os.WriteFile(path, []byte(script), 0700); err != nil { //nolint:gosec // trusted scanner executable fixture
				t.Fatal(err)
			}
			got := (Gitleaks{}).scan(context.Background(), path, []byte("PRIVATE"))
			if got.Reason != tt.reason || strings.Contains(got.ScannerVersion+got.Reason, "PRIVATE") {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

const modernScannerHelp = "gitleaks stdin --config --report-format --report-path --redact --exit-code --no-banner --ignore-gitleaks-allow --max-decode-depth"
const legacyScannerHelp = "gitleaks detect --pipe --config --report-format --report-path --redact --exit-code --no-banner"

func TestScannerCapabilities(t *testing.T) {
	for _, tt := range []struct {
		name, modern, legacy string
		want                 string
	}{
		{"modern", modernScannerHelp, "", "stdin"},
		{"ubuntu", "", legacyScannerHelp, "detect-pipe"},
		{"missing report flag", strings.ReplaceAll(modernScannerHelp, "--report-path", ""), "", ""},
		{"missing pipe flag", "", strings.ReplaceAll(legacyScannerHelp, "--pipe", ""), ""},
		{"unknown help", "gitleaks --help", "gitleaks --help", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(_ context.Context, _ string, _ string, _ []byte, args ...string) ([]byte, error) {
				if !reflect.DeepEqual(args[1:], []string{"--help"}) {
					t.Fatal("unexpected discovery arguments")
				}
				if args[0] == "stdin" {
					return []byte(tt.modern), nil
				}
				return []byte(tt.legacy), nil
			}
			profile, ok := discoverScannerProfile(context.Background(), runner, "/scanner", t.TempDir())
			if ok != (tt.want != "") || profile.name != tt.want {
				t.Fatalf("got %+v, %v", profile, ok)
			}
			if !ok {
				return
			}
			assertScannerProfileFlags(t, profile)
		})
	}
}

func TestScannerRejectsInlineSuppression(t *testing.T) {
	for _, input := range []string{"https://example.com?token=PRIVATE&gitleaks:allow", "gitleaks:allow\nPRIVATE", "GITLEAKS:ALLOW", "PRIVATE gitleaks:allow"} {
		t.Run(input, func(t *testing.T) {
			scanner := Gitleaks{run: func(context.Context, string, string, []byte, ...string) ([]byte, error) {
				t.Fatal("suppression input reached scanner")
				return nil, nil
			}}
			if got := scanner.scan(context.Background(), "/scanner", []byte(input)); got != decline("get.validation_incomplete") {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestLegacyScannerActualInvocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitleaks")
	script := `#!/bin/sh
if [ "$1" = stdin ]; then exit 1; fi
if [ "$2" = --help ]; then echo '` + legacyScannerHelp + `'; exit; fi
if [ "$1" = version ]; then echo 'version is set by build process'; exit; fi
if [ "$1" != detect ] || [ "$2" != --pipe ]; then exit 3; fi
for arg do
 case "$arg" in
 --report-path=*) report=${arg#--report-path=};;
 --ignore-gitleaks-allow|--max-decode-depth=*|--redact=*) exit 3;;
 esac
done
input=$(/bin/cat)
if [ "$input" = PRIVATE ] && [ -z "$GITLEAKS_CONFIG" ]; then echo '[]' > "$report"; else exit 3; fi
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil { //nolint:gosec // trusted executable fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GITLEAKS_CONFIG", "PRIVATE")
	got := (Gitleaks{}).Scan(context.Background(), []byte("PRIVATE"))
	if got.Outcome != Approve || got.ScannerVersion != "unavailable" || !strings.HasSuffix(got.ScannerConfig, "/detect-pipe") {
		t.Fatalf("got %+v", got)
	}
}

func assertScannerConfiguration(t *testing.T, dir string) {
	t.Helper()
	config, err := os.ReadFile(filepath.Join(dir, "rules.toml")) //nolint:gosec // test-owned scanner configuration
	if err != nil || string(config) != scannerConfig {
		t.Fatal("config missing")
	}
}

func assertScannerProfileFlags(t *testing.T, profile scannerProfile) {
	t.Helper()
	args := strings.Join(profile.scanArgs("/scratch"), " ")
	if profile.name == "detect-pipe" && (strings.Contains(args, "--ignore-gitleaks-allow") || strings.Contains(args, "--max-decode-depth") || strings.Contains(args, "--redact=")) {
		t.Fatal("legacy scanner got unsupported flags")
	}
}
