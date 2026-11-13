package approval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/roomscript/coderoom/internal/agent"
)

func (p Policy) evaluateFiles(ctx context.Context, operation agent.FileOperation) Result {
	if !operation.AllTargetsKnown || len(operation.Paths) == 0 {
		return review("policy.evidence_incomplete")
	}
	for _, path := range operation.Paths {
		result := p.evaluatePath(ctx, operation, path)
		if result.Outcome != Approve {
			return result
		}
	}
	return approve("file.project_contained")
}

func (p Policy) evaluatePath(ctx context.Context, operation agent.FileOperation, path string) Result {
	resolved, ok := p.resolveProjectPath(operation.Cwd, path, operation.Write)
	if !ok {
		return review("file.path_unvalidated")
	}
	if !isSupportedFileTarget(resolved, operation.Write) {
		return review("file.target_unsupported")
	}
	if !operation.Write {
		return approve("file.project_contained")
	}
	if protected(path) || protected(resolved) {
		return review("file.protected")
	}
	if p.Ignored == nil {
		return review("file.ignored_or_unknown")
	}
	return p.evaluateIgnored(ctx, operation.Cwd, path, resolved)
}

func (p Policy) evaluateIgnored(ctx context.Context, cwd string, paths ...string) Result {
	for _, target := range paths {
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		ignored, err := p.Ignored(ctx, target)
		if err != nil || ignored {
			return review("file.ignored_or_unknown")
		}
	}
	return approve("file.project_contained")
}

func isSupportedFileTarget(path string, write bool) bool {
	info, err := os.Stat(path)
	if err != nil {
		return write && os.IsNotExist(err)
	}
	if !info.Mode().IsRegular() {
		return false
	}
	if !write {
		return true
	}
	// A contained name can alias a protected or outside-project inode. Require
	// a known single link before granting a write, even if this name is ordinary.
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func protected(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		// Protect every case variant on all filesystems rather than relying on
		// EvalSymlinks to canonicalize casing or probing filesystem behavior.
		if strings.EqualFold(part, "AGENTS.md") || strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

func (p Policy) resolveProjectPath(cwd, path string, write bool) (string, bool) {
	if !validScope(p.ProjectRoot, cwd, path) {
		return "", false
	}
	root, err := filepath.EvalSymlinks(p.ProjectRoot)
	if err != nil || !filepath.IsAbs(root) {
		return "", false
	}
	base, err := filepath.EvalSymlinks(cwd)
	if err != nil || !within(root, base) {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	resolved, ok := resolveTarget(path, write)
	return resolved, ok && within(root, resolved)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveTarget(path string, write bool) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, true
	}
	if !write || !os.IsNotExist(err) {
		return "", false
	}
	// Resolve the nearest existing ancestor, retaining nonexistent suffixes.
	if _, err := os.Lstat(path); err == nil {
		return "", false
	} // dangling symlink
	parent := filepath.Dir(path)
	if parent == path {
		return "", false
	}
	base, ok := resolveTarget(parent, true)
	return filepath.Join(base, filepath.Base(path)), ok
}

func validScope(root, cwd, path string) bool {
	if root == "" || cwd == "" || path == "" || strings.ContainsRune(path, 0) {
		return false
	}
	// Do not clean symlink/.. traversal before resolving it.
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
