package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the participant configuration loaded at startup. It is never
// reloaded; ChangedSources reports edits made since.
type Config struct {
	root         string
	defaults     Defaults
	participants map[string]loadedParticipant
}

type loadedParticipant struct {
	config  ParticipantConfig
	sources []source // the participant file, then its role prompt if any
}

// source is a file a participant's config was built from, as it was read.
type source struct {
	path        string // relative to the repo root
	fingerprint fingerprint
}

// fingerprint identifies a file's content. The zero value means the file
// does not exist.
type fingerprint struct {
	exists     bool
	unreadable bool
	sum        [sha256.Size]byte
}

// Load reads every participant file under root and every role prompt they
// reference. It reports every broken file, not only the first.
//
// When only some files are broken, Load returns both the error and a Config
// holding the files that loaded, so their backends can be validated in the
// same report. Such a Config is for diagnostics only. When nothing could be
// read at all, the Config is nil.
func Load(root string, defaults Defaults) (*Config, error) {
	if strings.TrimSpace(defaults.Backend.Name) == "" {
		return nil, errMissingDefaultBackend
	}
	aliases, err := readParticipantAliases(root)
	if err != nil {
		return nil, err
	}
	c := &Config{root: root, defaults: defaults, participants: make(map[string]loadedParticipant, len(aliases))}
	var errs []error
	for _, alias := range aliases {
		p, err := c.loadParticipant(alias)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.participants[alias] = p
	}
	return c, errors.Join(errs...)
}

// readParticipantAliases returns the alias of each participant file, sorted.
// A missing participants directory means no files.
func readParticipantAliases(root string) ([]string, error) {
	dir := filepath.Join(configDirName, participantsDirName)
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read participant configs %q: %w", dir, err)
	}
	var aliases []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), participantFileExt) {
			continue
		}
		aliases = append(aliases, strings.TrimSuffix(e.Name(), participantFileExt))
	}
	return aliases, nil
}

func (c *Config) loadParticipant(alias string) (loadedParticipant, error) {
	path := participantPath(alias)
	data, err := os.ReadFile(c.absPath(path))
	if err != nil {
		return loadedParticipant{}, fmt.Errorf("%s: %w", path, err)
	}
	def, err := parseParticipantDefinition(data)
	if err != nil {
		return loadedParticipant{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateParticipantDefinition(alias, def); err != nil {
		return loadedParticipant{}, fmt.Errorf("%s: %w", path, err)
	}
	sources := []source{{path: path, fingerprint: fingerprintOf(data)}}

	rolePrompt, roleSource, err := c.loadRolePrompt(def.Role)
	if err != nil {
		return loadedParticipant{}, fmt.Errorf("%s: %w", path, err)
	}
	if roleSource != nil {
		sources = append(sources, *roleSource)
	}

	backend := BackendRequest{Name: def.Backend, Source: path}
	if backend.Name == "" {
		backend = c.defaults.Backend
	}
	return loadedParticipant{
		config: ParticipantConfig{
			Alias:          def.Alias,
			Role:           def.Role,
			Backend:        backend,
			Prompt:         buildPrompt(def.Alias, def.Role, rolePrompt),
			LoadedFromDisk: true,
		},
		sources: sources,
	}, nil
}

// loadRolePrompt returns the prompt for role and the file it came from, or
// nothing when role is empty.
func (c *Config) loadRolePrompt(role string) (string, *source, error) {
	if role == "" {
		return "", nil, nil
	}
	path := rolePromptPath(role)
	data, err := os.ReadFile(c.absPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("%w: %q for role %q", errRolePromptNotFound, path, role)
	}
	if err != nil {
		return "", nil, fmt.Errorf("read role prompt %q: %w", path, err)
	}
	return strings.TrimSpace(string(data)), &source{path: path, fingerprint: fingerprintOf(data)}, nil
}

// Participant returns the loaded config for alias, or the default config when
// no file exists for it. It only fails for an invalid alias.
func (c *Config) Participant(alias string) (ParticipantConfig, error) {
	alias, err := normalizeAlias(alias)
	if err != nil {
		return ParticipantConfig{}, err
	}
	if p, ok := c.participants[alias]; ok {
		return p.config, nil
	}
	return ParticipantConfig{
		Alias:   alias,
		Backend: c.defaults.Backend,
		Prompt:  buildPrompt(alias, "", ""),
	}, nil
}

// ConfiguredBackends maps each source to the backend name it requests: every
// participant file that sets a backend, and the default.
func (c *Config) ConfiguredBackends() map[string]string {
	backends := map[string]string{c.defaults.Backend.Source: c.defaults.Backend.Name}
	for _, p := range c.participants {
		backends[p.config.Backend.Source] = p.config.Backend.Name
	}
	return backends
}

// ChangedSources returns the repo-relative paths of alias's sources that
// differ on disk from what Load read: edited, deleted, or a participant file
// created after startup. It reads the files but does not parse them. An
// invalid alias has no sources.
func (c *Config) ChangedSources(alias string) []string {
	alias, err := normalizeAlias(alias)
	if err != nil {
		return nil
	}
	p, ok := c.participants[alias]
	if !ok {
		p.sources = []source{{path: participantPath(alias)}}
	}
	var changed []string
	for _, src := range p.sources {
		if readFingerprint(c.absPath(src.path)) != src.fingerprint {
			changed = append(changed, src.path)
		}
	}
	return changed
}

func (c *Config) absPath(path string) string {
	return filepath.Join(c.root, path)
}

func normalizeAlias(alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	if err := validatePathSegment(alias, errInvalidParticipantAlias); err != nil {
		return "", err
	}
	return alias, nil
}

func fingerprintOf(data []byte) fingerprint {
	return fingerprint{exists: true, sum: sha256.Sum256(data)}
}

// readFingerprint reads path's current fingerprint. A file that exists but
// cannot be read never matches a loaded fingerprint.
func readFingerprint(path string) fingerprint {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return fingerprint{}
	}
	if err != nil {
		return fingerprint{exists: true, unreadable: true}
	}
	return fingerprintOf(data)
}
