package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/agent/codex"
)

const (
	promptFileName       = "prompt.md"
	conversationFileName = "conversation.md"
)

// ReadInputDir loads one transcript authoring scenario from a case directory.
func ReadInputDir(caseDir string) (Input, error) {
	hasPrompt := fileExists(filepath.Join(caseDir, promptFileName))
	hasConversation := fileExists(filepath.Join(caseDir, conversationFileName))
	switch {
	case hasPrompt && hasConversation:
		return Input{}, errors.New("found both prompt.md and conversation.md")
	case hasPrompt:
		return readPromptInput(filepath.Join(caseDir, promptFileName))
	case hasConversation:
		return readConversationInput(caseDir)
	default:
		return Input{}, errors.New("missing prompt.md or conversation.md")
	}
}

func readPromptInput(path string) (Input, error) {
	config, body, err := readMarkdownScenarioFile(path)
	if err != nil {
		return Input{}, err
	}
	if strings.TrimSpace(body) == "" {
		return Input{}, errors.New("empty prompt body")
	}
	return Input{
		Config: config,
		Actions: []Action{{
			Kind: "prompt",
			Text: strings.TrimSpace(body),
		}},
	}, nil
}

func readConversationInput(caseDir string) (Input, error) {
	config, err := readConversationConfig(filepath.Join(caseDir, conversationFileName))
	if err != nil {
		return Input{}, err
	}
	actionFiles, err := resolveConversationActionFiles(caseDir)
	if err != nil {
		return Input{}, err
	}
	actions := make([]Action, 0, len(actionFiles))
	for _, path := range actionFiles {
		action, err := readConversationAction(path)
		if err != nil {
			return Input{}, err
		}
		actions = append(actions, action)
	}
	return Input{Config: config, Actions: actions}, nil
}

func readConversationConfig(path string) (Config, error) {
	config, body, err := readMarkdownScenarioFile(path)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(body) != "" {
		return Config{}, errors.New("conversation.md body must be empty")
	}
	return config, nil
}

func readConversationAction(path string) (Action, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Action{}, fmt.Errorf("open conversation action %q: %w", path, err)
	}
	parts := bytes.SplitN(data, []byte("\n---\n"), 2)
	if len(parts) == 1 {
		body := strings.TrimSpace(string(parts[0]))
		if body == "" {
			return Action{}, errors.New("empty conversation action body")
		}
		return Action{Kind: "prompt", Text: body}, nil
	}
	action, err := parseConversationActionFrontMatter(parts[0])
	if err != nil {
		return Action{}, fmt.Errorf("parse conversation action %q: %w", path, err)
	}
	action.Text = strings.TrimSpace(string(parts[1]))
	if action.Text == "" {
		return Action{}, errors.New("empty conversation action body")
	}
	if action.Kind == "" {
		action.Kind = "prompt"
	}
	return action, nil
}

func readMarkdownScenarioFile(path string) (Config, string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Config{}, "", fmt.Errorf("open scenario file %q: %w", path, err)
	}
	defer closeInputFile(file)
	return parseMarkdownScenarioFile(file)
}

func parseMarkdownScenarioFile(r io.Reader) (Config, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, "", fmt.Errorf("read scenario file: %w", err)
	}
	parts := bytes.SplitN(data, []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return Config{}, "", errors.New("missing front matter delimiter")
	}
	config, err := parseInputFrontMatter(parts[0])
	if err != nil {
		return Config{}, "", err
	}
	return config, string(parts[1]), nil
}

func parseInputFrontMatter(raw []byte) (Config, error) {
	var config Config
	for _, line := range strings.Split(string(raw), "\n") {
		if err := applyInputFrontMatterLine(&config, line); err != nil {
			return Config{}, err
		}
	}
	return config, nil
}

func applyInputFrontMatterLine(config *Config, line string) error {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
		return nil
	}
	key, decoded, err := parseInputFrontMatterScalar(trimmed)
	if err != nil {
		return err
	}
	return assignInputFrontMatterValue(config, key, decoded)
}

func parseInputFrontMatterScalar(trimmed string) (string, string, error) {
	key, value, ok := strings.Cut(trimmed, ":")
	if !ok {
		return "", "", fmt.Errorf("parse front matter line %q", trimmed)
	}
	normalizedKey := strings.TrimSpace(key)
	decoded, err := unquoteScalar(strings.TrimSpace(value))
	if err != nil {
		return "", "", fmt.Errorf("decode front matter value for %q: %w", normalizedKey, err)
	}
	return normalizedKey, decoded, nil
}

func assignInputFrontMatterValue(config *Config, key, decoded string) error {
	switch key {
	case "model":
		config.Model = decoded
	case "developer_instructions":
		config.DeveloperInstructions = decoded
	case "ask_for_approval":
		config.AskForApproval = codex.AskForApprovalPolicy(decoded)
	case "sandbox":
		config.Sandbox = codex.SandboxMode(decoded)
	case "reasoning_effort":
		config.ReasoningEffort = codex.ReasoningEffort(decoded)
	case "reasoning_summary":
		config.ReasoningSummary = codex.ReasoningSummary(decoded)
	default:
		return fmt.Errorf("unknown front matter key %q", key)
	}
	return nil
}

func parseConversationActionFrontMatter(raw []byte) (Action, error) {
	var action Action
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, decoded, err := parseInputFrontMatterScalar(trimmed)
		if err != nil {
			return Action{}, err
		}
		switch key {
		case "kind":
			action.Kind = decoded
		default:
			return Action{}, fmt.Errorf("unknown conversation action key %q", key)
		}
	}
	return action, nil
}

func resolveConversationActionFiles(caseDir string) ([]string, error) {
	names, err := listDirEntries(caseDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range names {
		if strings.HasPrefix(name, "conversation-") && strings.HasSuffix(name, ".md") {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	if len(files) == 0 {
		return nil, errors.New("missing conversation action files")
	}
	for index, name := range files {
		expected := fmt.Sprintf("conversation-%02d.md", index+1)
		if name != expected {
			return nil, fmt.Errorf("expected %s, found %s", expected, name)
		}
	}
	paths := make([]string, 0, len(files))
	for _, name := range files {
		paths = append(paths, filepath.Join(caseDir, name))
	}
	return paths, nil
}

func listDirEntries(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read directory %q: %w", root, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names, nil
}

func unquoteScalar(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value[0] != '"' {
		return value, nil
	}
	decoded, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("parse quoted value: %w", err)
	}
	return decoded, nil
}

func closeInputFile(file *os.File) {
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close file: %v\n", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
