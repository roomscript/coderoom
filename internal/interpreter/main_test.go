package interpreter

import (
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func located[T any](value T) promptlang.Located[T] {
	return promptlang.Located[T]{Value: value}
}
