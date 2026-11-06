//go:build windows && 386

package gmod

import (
	"errors"
	"testing"
)

func TestMissingProcessIsDistinctFromEnumerationFailure(t *testing.T) {
	const absent = "gmod-mcp-definitely-absent-test.exe"
	if _, err := findAllProcessIDsByName(absent); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("missing process should use ErrProcessNotFound: %v", err)
	}
	status, err := Status(absent)
	if err != nil || status.Running || status.SessionType != "not_running" {
		t.Fatalf("wrong missing-process status: %+v, %v", status, err)
	}
}
