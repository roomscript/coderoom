package interpreter

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const (
	interpreterPackage = "github.com/trigosec/coderoom/internal/interpreter"
	uiPackage          = "github.com/trigosec/coderoom/internal/ui"
	sessionPackage     = "github.com/trigosec/coderoom/internal/session"
	agentPackage       = "github.com/trigosec/coderoom/internal/agent"
)

type architecturePackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func TestArchitecture_interpreterHasNoPresentationDependencies(t *testing.T) {
	for _, pkg := range listArchitecturePackages(t) {
		for _, dependency := range forbiddenArchitectureDependencies(pkg) {
			t.Errorf("%s depends on forbidden package %s", pkg.ImportPath, dependency)
		}
	}
}

func listArchitecturePackages(t *testing.T) []architecturePackage {
	t.Helper()
	// Production Imports/Deps deliberately exclude test fixture imports.
	command := exec.CommandContext(t.Context(), "go", "list", "-deps", "-json=ImportPath,Imports,Deps", "./...")
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

func forbiddenArchitectureDependencies(pkg architecturePackage) []string {
	var forbidden []string
	if withinPackage(pkg.ImportPath, interpreterPackage) {
		for _, dependency := range pkg.Deps {
			if forbiddenInterpreterImport(dependency) {
				forbidden = append(forbidden, dependency)
			}
		}
	}
	return forbidden
}

func forbiddenInterpreterImport(path string) bool {
	roots := []string{
		uiPackage,
		"charm.land/bubbletea", "charm.land/bubbles", "charm.land/lipgloss",
		"github.com/charmbracelet/bubbletea", "github.com/charmbracelet/bubbles", "github.com/charmbracelet/lipgloss",
	}
	for _, root := range roots {
		if withinPackage(path, root) {
			return true
		}
	}
	return false
}

func withinPackage(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func TestForbiddenArchitectureDependencies(t *testing.T) {
	tests := []struct {
		name          string
		pkg           architecturePackage
		wantForbidden bool
	}{
		{name: "transitive UI dependency", pkg: architecturePackage{ImportPath: interpreterPackage, Deps: []string{uiPackage + "/palette"}}, wantForbidden: true},
		{name: "interpreter subpackage", pkg: architecturePackage{ImportPath: interpreterPackage + "/workflow", Deps: []string{uiPackage}}, wantForbidden: true},
		{name: "interpreter runtime dependencies allowed", pkg: architecturePackage{ImportPath: interpreterPackage, Deps: []string{sessionPackage, agentPackage}}},
		{name: "similarly named package allowed", pkg: architecturePackage{ImportPath: interpreterPackage, Deps: []string{uiPackage + "helpers", "charm.land/bubbleteax"}}},
		{name: "other package unrestricted", pkg: architecturePackage{ImportPath: sessionPackage, Imports: []string{agentPackage}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forbidden := forbiddenArchitectureDependencies(test.pkg)
			if (len(forbidden) != 0) != test.wantForbidden {
				t.Fatalf("forbidden dependencies = %v, want rejection = %v", forbidden, test.wantForbidden)
			}
		})
	}
}

func TestForbiddenArchitectureDependencies_terminalFamilies(t *testing.T) {
	families := []string{"charm.land/bubbletea", "charm.land/bubbles", "charm.land/lipgloss", "github.com/charmbracelet/bubbletea", "github.com/charmbracelet/bubbles", "github.com/charmbracelet/lipgloss"}
	for _, family := range families {
		for _, suffix := range []string{"", "/v2", "/v2/component"} {
			dependency := family + suffix
			t.Run(dependency, func(t *testing.T) {
				pkg := architecturePackage{ImportPath: interpreterPackage, Deps: []string{dependency}}
				forbidden := forbiddenArchitectureDependencies(pkg)
				if len(forbidden) != 1 || forbidden[0] != dependency {
					t.Fatalf("forbidden dependencies = %v, want [%s]", forbidden, dependency)
				}
			})
		}
	}
}
