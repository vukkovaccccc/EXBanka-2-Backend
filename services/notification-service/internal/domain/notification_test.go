package domain

import (
	"strings"
	"testing"
)

func TestErrUnknownEventType_Error(t *testing.T) {
	err := ErrUnknownEventType{Type: "UNKNOWN_EVENT"}
	msg := err.Error()
	if !strings.Contains(msg, "UNKNOWN_EVENT") {
		t.Errorf("expected error message to contain type, got: %s", msg)
	}
}

func TestErrUnknownEventType_EmptyType(t *testing.T) {
	err := ErrUnknownEventType{Type: ""}
	msg := err.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
}
