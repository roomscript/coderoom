package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const (
	sessionPackage = "github.com/trigosec/coderoom/internal/session"
	agentPackage   = "github.com/trigosec/coderoom/internal/agent"
)

type architecturePackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func TestArchitecture_UIHasNoDirectRuntimeImports(t *testing.T) {
	for _, pkg := range listArchitecturePackages(t) {
		for _, dependency := range forbiddenUIImports(pkg.Imports) {
			t.Errorf("%s directly imports forbidden package %s", pkg.ImportPath, dependency)
		}
	}
}

func listArchitecturePackages(t *testing.T) []architecturePackage {
	t.Helper()
	// Production Imports/Deps deliberately exclude test fixture imports.
	command := exec.CommandContext(t.Context(), "go", "list", "-json=ImportPath,Imports", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list architecture packages: %v\n%s", err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []architecturePackage
	for {
		var pkg architecturePackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			return packages
		}
		if err != nil {
			t.Fatalf("decode architecture package: %v", err)
		}
		packages = append(packages, pkg)
	}
}

func forbiddenUIImports(imports []string) []string {
	var forbidden []string
	for _, path := range imports {
		if withinPackage(path, sessionPackage) || withinPackage(path, agentPackage) {
			forbidden = append(forbidden, path)
		}
	}
	return forbidden
}

func withinPackage(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func TestForbiddenUIImports(t *testing.T) {
	tests := []struct {
		name          string
		pkg           architecturePackage
		wantForbidden bool
	}{
		{name: "session import", pkg: architecturePackage{Imports: []string{sessionPackage}}, wantForbidden: true},
		{name: "agent import", pkg: architecturePackage{Imports: []string{agentPackage}}, wantForbidden: true},
		{name: "agent subpackage", pkg: architecturePackage{Imports: []string{agentPackage + "/codex"}}, wantForbidden: true},
		{name: "session subpackage", pkg: architecturePackage{Imports: []string{sessionPackage + "/routing"}}, wantForbidden: true},
		{name: "indirect runtime dependencies allowed", pkg: architecturePackage{Imports: []string{"github.com/trigosec/coderoom/internal/interpreter"}, Deps: []string{sessionPackage, agentPackage}}},
		{name: "similar package names allowed", pkg: architecturePackage{Imports: []string{sessionPackage + "helpers", agentPackage + "log"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forbidden := forbiddenUIImports(test.pkg.Imports)
			if (len(forbidden) != 0) != test.wantForbidden {
				t.Fatalf("forbidden imports = %v, want rejection = %v", forbidden, test.wantForbidden)
			}
		})
	}
}
