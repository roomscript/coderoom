package interpreter

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type executorBoundaryOperation struct{}

func (executorBoundaryOperation) apply(*interpreterExecutor) {}

var _ operation = executorBoundaryOperation{}

func TestInterpreter_containsOnlyCompositionFields(t *testing.T) {
	typeOfInterpreter := reflect.TypeOf(Interpreter{})
	want := map[string]reflect.Type{
		"model":    reflect.TypeOf((*interpreterModel)(nil)),
		"executor": reflect.TypeOf((*interpreterExecutor)(nil)),
	}
	if typeOfInterpreter.NumField() != len(want) {
		t.Fatalf("Interpreter fields = %d, want %d composition fields", typeOfInterpreter.NumField(), len(want))
	}
	for name, fieldType := range want {
		field, ok := typeOfInterpreter.FieldByName(name)
		if !ok {
			t.Fatalf("Interpreter is missing %s field", name)
		}
		if field.Type != fieldType {
			t.Fatalf("Interpreter.%s type = %v, want %v", name, field.Type, fieldType)
		}
	}
}

func TestInterpreter_hasNoPresentationDependencies(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("list interpreter dependencies: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if forbiddenInterpreterImport(dependency) {
			t.Errorf("interpreter depends on presentation package %q", dependency)
		}
	}
}

func forbiddenInterpreterImport(path string) bool {
	return strings.HasPrefix(path, "github.com/trigosec/coderoom/internal/ui") ||
		strings.HasPrefix(path, "charm.land/bubbletea") ||
		strings.HasPrefix(path, "charm.land/bubbles") ||
		strings.HasPrefix(path, "charm.land/lipgloss")
}
