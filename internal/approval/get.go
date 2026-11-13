package approval

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/roomscript/coderoom/internal/agent"
)

// Scanner inspects local request bytes. It never sends the HTTP request.
type Scanner interface {
	Discover() bool
	Scan(context.Context, []byte) Result
}

func (p Policy) evaluateHTTP(ctx context.Context, op agent.HTTPOperation) Result {
	if op.Method != "GET" {
		return review("get.method_unsupported")
	}
	scanner := p.Scanner
	if scanner == nil {
		scanner = Gitleaks{root: p.ProjectRoot}
	}
	if !scanner.Discover() {
		return review("get.scanner_missing")
	}
	input, ok := getInput(op)
	if !ok {
		return decline("get.validation_incomplete")
	}
	result := scanner.Scan(ctx, input)
	if ctx.Err() != nil {
		return review("policy.canceled")
	}
	return result
}

func getInput(op agent.HTTPOperation) ([]byte, bool) {
	if op.Dynamic || op.FollowsRedirects || len(op.URL)+len(op.Body) > maxCommandBytes {
		return nil, false
	}
	u, err := url.Parse(op.URL)
	if err != nil {
		return nil, false
	}
	if !validHTTPURL(u) {
		return nil, false
	}
	data, err := json.Marshal(op)
	if err != nil || len(data) > maxCommandBytes {
		return nil, false
	}
	text := string(data) + "\n" + op.URL + "\n" + op.Body
	for key, value := range op.Headers {
		text += "\n" + key + ": " + value
	}
	return decodedInput(text)
}

var encodedToken = regexp.MustCompile(`[A-Za-z0-9+/_-]{8,}={0,2}`)

func decodedInput(raw string) ([]byte, bool) {
	all := raw
	seen := map[string]bool{raw: true}
	queue := []string{raw}
	for depth := 0; len(queue) > 0; depth++ {
		var next []string
		for _, text := range queue {
			for _, value := range decodeVariants(text) {
				if seen[value] {
					continue
				}
				if depth >= 3 || len(all)+len(value)+1 > maxCommandBytes {
					return nil, false
				}
				seen[value] = true
				all += "\n" + value
				next = append(next, value)
			}
		}
		queue = next
	}
	return []byte(all), len(all) <= maxCommandBytes
}

func decodeVariants(text string) []string {
	values := []string{html.UnescapeString(text)}
	if v, err := url.PathUnescape(text); err == nil {
		values = append(values, v)
	}
	if v, err := url.QueryUnescape(text); err == nil {
		values = append(values, v)
	}
	for _, token := range encodedToken.FindAllString(text, -1) {
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			data, err := encoding.DecodeString(token)
			if err == nil && len(data) > 0 && utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
				values = append(values, string(data))
			}
		}
	}
	return values
}

func validHTTPURL(u *url.URL) bool {
	return u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func (p Policy) evaluateCurl(ctx context.Context, args []string) Result {
	op := agent.HTTPOperation{Method: "GET", Headers: map[string]string{}}
	op.Dynamic = !curlConfigurationKnown(args)
	for i := 0; i < len(args); i++ {
		consumed, result := applyCurlArgument(&op, args[i:])
		if result.Outcome != Approve {
			return result
		}
		i += consumed - 1
	}
	return p.evaluateHTTP(ctx, op)
}

func applyCurlArgument(op *agent.HTTPOperation, args []string) (int, Result) {
	switch args[0] {
	case "-s", "--silent", "-S", "--show-error", "-f", "--fail", "-q", "--disable":
	case "-L", "--location":
		op.FollowsRedirects = true
	case "-X", "--request", "-H", "--header":
		if len(args) < 2 {
			return 0, review("shell.arguments_unsupported")
		}
		return 2, applyCurlValue(op, args[0], args[1])
	default:
		if strings.HasPrefix(args[0], "-") || op.URL != "" {
			return 0, review("shell.arguments_unsupported")
		}
		op.URL = args[0]
	}
	return 1, approve("shell.supported")
}

func applyCurlValue(op *agent.HTTPOperation, option, value string) Result {
	if option == "-X" || option == "--request" {
		if value != "GET" {
			return review("get.method_unsupported")
		}
		return approve("shell.supported")
	}
	key, value, ok := strings.Cut(value, ":")
	if !ok || key == "" || strings.HasPrefix(key, "@") {
		op.Dynamic = true
		return approve("shell.supported")
	}
	if previous, exists := op.Headers[key]; exists {
		value = previous + "\n" + value
	}
	op.Headers[key] = value
	return approve("shell.supported")
}

func decline(reason string) Result { return Result{Outcome: Decline, Reason: reason} }

// Curl's optional config can add redirects, uploads or outbound data. Explicit
// -q as the first argument disables it; otherwise require known absent files.
func curlConfigurationKnown(args []string) bool {
	if len(args) > 0 && (args[0] == "-q" || args[0] == "--disable") {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	directories := []string{home, os.Getenv("CURL_HOME")}
	for _, dir := range directories {
		if dir != "" && !absentCurlConfig(dir, ".curlrc") {
			return false
		}
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	return absentCurlConfig(dir, "curlrc")
}

func absentCurlConfig(dir, name string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, name)) //nolint:gosec // metadata-only check of local curl config, never opens candidate data
	return os.IsNotExist(err)
}
