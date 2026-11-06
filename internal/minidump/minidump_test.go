package minidump

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"unicode/utf16"
)

func TestParseExceptionAndModuleOffset(t *testing.T) {
	data := make([]byte, 512)
	put32 := func(at int, value uint32) { binary.LittleEndian.PutUint32(data[at:], value) }
	put64 := func(at int, value uint64) { binary.LittleEndian.PutUint64(data[at:], value) }
	copy(data, "MDMP")
	put32(8, 3)
	put32(12, 32)
	// Exception, module list, and thread list streams.
	put32(32, 6)
	put32(36, 168)
	put32(40, 68)
	put32(44, 4)
	put32(48, 112)
	put32(52, 236)
	put32(56, 3)
	put32(60, 52)
	put32(64, 380)
	put32(68, 42)
	put32(76, 0xc0000005)
	put64(92, 0x10000882)
	put32(100, 2)
	put64(108, 1)
	put64(116, 0xdeadbeef)
	put32(236, 1)
	put64(240, 0x10000000)
	put32(248, 0x1000)
	put32(260, 440)
	put32(380, 1)
	put32(384, 42)
	put32(416, 8)
	put32(420, 432)
	put32(432, 0x10000123)
	name := utf16.Encode([]rune(`C:\Game\materialsystem.dll`))
	put32(440, uint32(len(name)*2))
	for i, char := range name {
		binary.LittleEndian.PutUint16(data[444+i*2:], char)
	}

	summary, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if summary.ExceptionName != "EXCEPTION_ACCESS_VIOLATION" || summary.ThreadID != 42 ||
		summary.FaultModule != "materialsystem.dll" || summary.FaultModuleOffset != 0x882 ||
		summary.AccessType != "write" || summary.AccessAddress == nil || *summary.AccessAddress != 0xdeadbeef {
		t.Fatalf("wrong exception summary: %+v", summary)
	}
	if len(summary.StackCandidates) != 1 || summary.StackCandidates[0].ModuleOffset != 0x123 {
		t.Fatalf("wrong stack candidates: %+v", summary.StackCandidates)
	}
}

func TestParseMalformedDump(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a dump"), append([]byte("MDMP"), make([]byte, 28)...)} {
		if _, err := Parse(bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatalf("accepted malformed dump %q", data)
		}
	}
}

func TestParseRealDumpWhenProvided(t *testing.T) {
	path := os.Getenv("GMOD_MINIDUMP_SAMPLE")
	if path == "" {
		t.Skip("set GMOD_MINIDUMP_SAMPLE to validate a real dump")
	}
	summary, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ExceptionCode == 0 || summary.ExceptionAddress == 0 {
		t.Fatalf("incomplete summary: %+v", summary)
	}
	t.Logf("real dump: %+v", summary)
}
