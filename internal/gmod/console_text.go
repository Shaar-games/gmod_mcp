package gmod

import (
	"regexp"
	"strings"
)

// engine.dll's crash-history strings can prefix each message with a sequence
// number and engine time, e.g. "2848(285.462200):  message".
var consoleMessagePrefix = regexp.MustCompile(`^\s*\d+\(\s*-?\d+(?:\.\d+)?\):[ \t]*`)

func consoleMessage(line string) string {
	return strings.TrimSpace(consoleMessagePrefix.ReplaceAllString(line, ""))
}
