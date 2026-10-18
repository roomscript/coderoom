// Package config loads repo-local coderoom configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	configDirName       = ".coderoom"
	participantsDirName = "participants"
	rolePromptsDirName  = "prompts/roles"
	participantFileExt  = ".yaml"
)

var (
	errInvalidParticipantAlias = errors.New("invalid participant alias")
	errInvalidParticipantRole  = errors.New("invalid participant role")
	errInvalidParticipantYAML  = errors.New("invalid participant yaml")
	errRolePromptNotFound      = errors.New("role prompt not found")
	errMissingDefaultBackend   = errors.New("default backend must not be empty")
)

// ParticipantConfig is the resolved invite-time configuration for a participant.
type ParticipantConfig struct {
	Alias          string
	Role           string
	Backend        BackendRequest
	Prompt         string
	LoadedFromDisk bool
}

// BackendRequest is a backend name as configured, and where it was set.
// config does not check that the name exists.
type BackendRequest struct {
	Name string
	// Source is a participant file path relative to the repo root, or the
	// default's source, e.g. "CODEROOM_BACKEND".
	Source string
}

// Defaults are the values a participant gets when its file does not set them,
// or when it has no file.
type Defaults struct {
	Backend BackendRequest // Name is required
}

type participantDefinition struct {
	Alias   string
	Role    string
	Backend string
}

type rawParticipantDefinition struct {
	Alias   string  `yaml:"alias"`
	Role    *string `yaml:"role"`
	Backend *string `yaml:"backend"`
}

func parseParticipantDefinition(data []byte) (participantDefinition, error) {
	var raw rawParticipantDefinition

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return participantDefinition{}, fmt.Errorf("%w: %v", errInvalidParticipantYAML, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if !errors.Is(err, io.EOF) {
			return participantDefinition{}, fmt.Errorf("%w: %v", errInvalidParticipantYAML, err)
		}
	} else {
		return participantDefinition{}, fmt.Errorf("%w: multiple yaml documents are not supported", errInvalidParticipantYAML)
	}

	return participantDefinition{
		Alias:   strings.TrimSpace(raw.Alias),
		Role:    trimOptional(raw.Role),
		Backend: trimOptional(raw.Backend),
	}, nil
}

func trimOptional(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// validateParticipantDefinition checks a parsed file against the alias its
// filename gives.
func validateParticipantDefinition(alias string, def participantDefinition) error {
	if def.Alias == "" {
		return fmt.Errorf("%w: participant alias must not be empty", errInvalidParticipantYAML)
	}
	if err := validatePathSegment(def.Alias, errInvalidParticipantAlias); err != nil {
		return err
	}
	if def.Alias != alias {
		return fmt.Errorf("%w: alias %q does not match filename %q", errInvalidParticipantYAML, def.Alias, alias)
	}
	if def.Role != "" {
		if err := validatePathSegment(def.Role, errInvalidParticipantRole); err != nil {
			return err
		}
	}
	return nil
}

// participantPath returns the repo-relative path of alias's file.
func participantPath(alias string) string {
	return filepath.Join(configDirName, participantsDirName, alias+participantFileExt)
}

// rolePromptPath returns the repo-relative path of role's prompt.
func rolePromptPath(role string) string {
	return filepath.Join(configDirName, rolePromptsDirName, role+".md")
}

func validatePathSegment(value string, kind error) error {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return fmt.Errorf("%w: value must not be empty", kind)
	case value == "." || value == "..":
		return fmt.Errorf("%w: %q is not a valid path segment", kind, value)
	case strings.Contains(value, "/"), strings.Contains(value, `\`):
		return fmt.Errorf("%w: %q is not a valid path segment", kind, value)
	}
	return nil
}

func buildPrompt(alias, role, rolePrompt string) string {
	parts := []string{
		fmt.Sprintf("Your name is %s.\nYou will be referred to as %s or @%s.", alias, alias, alias),
	}
	if role != "" {
		parts = append(parts, fmt.Sprintf("You are a %s.", role))
	}
	if rolePrompt != "" {
		parts = append(parts, rolePrompt)
	}
	return strings.Join(parts, "\n\n")
}
