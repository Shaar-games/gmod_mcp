//go:build windows && 386

package gmod

import (
	"reflect"
	"testing"
)

func TestConsoleDisplayLinesStripsDumpPrefixAndEmptyEntries(t *testing.T) {
	raw := []string{
		"5(3.000000):  third ",
		"4(2.000000):  ",
		"3(1.500000):  second",
		"2(1.000000):  first",
	}

	got := ConsoleDisplayLines(raw, 0)
	want := []string{"first", "second", "third "}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConsoleDisplayLines() = %#v, want %#v", got, want)
	}
}

func TestConsoleDisplayLinesKeepsPlainLines(t *testing.T) {
	raw := []string{
		"2(2.000000):  second",
		"plain line",
	}

	got := ConsoleDisplayLines(raw, 1)
	want := []string{"second"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConsoleDisplayLines() = %#v, want %#v", got, want)
	}
}
