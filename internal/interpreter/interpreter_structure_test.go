package interpreter

import (
	"reflect"
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
