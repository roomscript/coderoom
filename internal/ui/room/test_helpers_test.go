package room

import "testing"

func newTestModel(t *testing.T) Model {
	t.Helper()
	m := NewPresenter(nil, nil, "")
	return m
}
