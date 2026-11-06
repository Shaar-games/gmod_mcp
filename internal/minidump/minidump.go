// Package minidump extracts a small, bounded crash summary from Windows dumps.
// It does not interpret symbols or claim that stack-address candidates form a
// reliable call stack.
package minidump

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf16"
)

const (
	maxStreams         = 128
	maxModules         = 4096
	maxNameBytes       = 4096
	maxStackBytes      = 64 * 1024
	maxStackCandidates = 16
)

type StackCandidate struct {
	StackOffset  uint32 `json:"stackOffset"`
	Module       string `json:"module"`
	ModuleOffset uint64 `json:"moduleOffset"`
}

type Summary struct {
	ExceptionCode     uint32           `json:"exceptionCode"`
	ExceptionName     string           `json:"exceptionName,omitempty"`
	ThreadID          uint32           `json:"threadId"`
	ExceptionAddress  uint64           `json:"exceptionAddress"`
	FaultModule       string           `json:"faultModule,omitempty"`
	FaultModuleOffset uint64           `json:"faultModuleOffset,omitempty"`
	AccessType        string           `json:"accessType,omitempty"`
	AccessAddress     *uint64          `json:"accessAddress,omitempty"`
	StackCandidates   []StackCandidate `json:"stackCandidates,omitempty"`
}

type section struct{ offset, size uint64 }
type module struct {
	base uint64
	size uint32
	name string
}

func ParseFile(path string) (Summary, error) {
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Summary{}, err
	}
	return Parse(f, info.Size())
}

func readAt(r io.ReaderAt, fileSize int64, off, length uint64) ([]byte, error) {
	if fileSize < 0 || off > uint64(fileSize) || length > uint64(fileSize)-off {
		return nil, fmt.Errorf("minidump range %d+%d exceeds file size %d", off, length, fileSize)
	}
	buf := make([]byte, length)
	_, err := r.ReadAt(buf, int64(off))
	return buf, err
}

func u32(data []byte, at int) uint32 { return binary.LittleEndian.Uint32(data[at : at+4]) }
func u64(data []byte, at int) uint64 { return binary.LittleEndian.Uint64(data[at : at+8]) }

func Parse(r io.ReaderAt, fileSize int64) (Summary, error) {
	header, err := readAt(r, fileSize, 0, 32)
	if err != nil {
		return Summary{}, err
	}
	if string(header[:4]) != "MDMP" {
		return Summary{}, errors.New("not a Windows minidump")
	}
	count := u32(header, 8)
	if count > maxStreams {
		return Summary{}, fmt.Errorf("minidump has too many streams: %d", count)
	}
	directory, err := readAt(r, fileSize, uint64(u32(header, 12)), uint64(count)*12)
	if err != nil {
		return Summary{}, err
	}
	streams := map[uint32]section{}
	for i := uint32(0); i < count; i++ {
		at := int(i) * 12
		kind := u32(directory, at)
		if kind == 0 { // Multiple unused directory slots are valid.
			continue
		}
		if _, duplicate := streams[kind]; duplicate {
			return Summary{}, fmt.Errorf("duplicate minidump stream %d", kind)
		}
		streams[kind] = section{uint64(u32(directory, at+8)), uint64(u32(directory, at+4))}
	}
	exceptionStream, ok := streams[6]
	if !ok || exceptionStream.size < 168 {
		return Summary{}, errors.New("minidump has no complete exception stream")
	}
	ex, err := readAt(r, fileSize, exceptionStream.offset, 168)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{ThreadID: u32(ex, 0), ExceptionCode: u32(ex, 8), ExceptionAddress: u64(ex, 24)}
	summary.ExceptionName = map[uint32]string{
		0xc0000005: "EXCEPTION_ACCESS_VIOLATION", 0xc000001d: "EXCEPTION_ILLEGAL_INSTRUCTION",
		0xc0000094: "EXCEPTION_INT_DIVIDE_BY_ZERO", 0xc00000fd: "EXCEPTION_STACK_OVERFLOW",
		0xc0000374: "EXCEPTION_HEAP_CORRUPTION", 0xc0000409: "EXCEPTION_STACK_BUFFER_OVERRUN",
		0x80000003: "EXCEPTION_BREAKPOINT",
	}[summary.ExceptionCode]
	if summary.ExceptionCode == 0xc0000005 && u32(ex, 32) >= 2 {
		value := u64(ex, 48)
		summary.AccessAddress = &value
		switch u64(ex, 40) {
		case 0:
			summary.AccessType = "read"
		case 1:
			summary.AccessType = "write"
		case 8:
			summary.AccessType = "execute"
		default:
			summary.AccessType = "unknown"
		}
	}
	modules, err := parseModules(r, fileSize, streams[4])
	if err != nil {
		return summary, err
	}
	if owner, offset, found := owningModule(modules, summary.ExceptionAddress); found {
		summary.FaultModule, summary.FaultModuleOffset = owner, offset
	}
	if stack, err := readThreadStack(r, fileSize, streams[3], summary.ThreadID); err == nil {
		for at := 0; at+4 <= len(stack) && len(summary.StackCandidates) < maxStackCandidates; at += 4 {
			address := uint64(u32(stack, at))
			if owner, offset, found := owningModule(modules, address); found {
				summary.StackCandidates = append(summary.StackCandidates, StackCandidate{uint32(at), owner, offset})
			}
		}
	}
	return summary, nil
}

func parseModules(r io.ReaderAt, fileSize int64, stream section) ([]module, error) {
	if stream.size == 0 {
		return nil, nil
	}
	if stream.size < 4 {
		return nil, errors.New("truncated module list")
	}
	header, err := readAt(r, fileSize, stream.offset, 4)
	if err != nil {
		return nil, err
	}
	count := u32(header, 0)
	if count > maxModules || uint64(count)*108+4 > stream.size {
		return nil, errors.New("invalid module list size")
	}
	entries, err := readAt(r, fileSize, stream.offset+4, uint64(count)*108)
	if err != nil {
		return nil, err
	}
	modules := make([]module, 0, count)
	for i := uint32(0); i < count; i++ {
		at := int(i) * 108
		nameAt := uint64(u32(entries, at+20))
		lengthBytes, err := readAt(r, fileSize, nameAt, 4)
		if err != nil {
			return nil, err
		}
		length := u32(lengthBytes, 0)
		if length > maxNameBytes || length%2 != 0 {
			return nil, errors.New("invalid module name length")
		}
		encoded, err := readAt(r, fileSize, nameAt+4, uint64(length))
		if err != nil {
			return nil, err
		}
		chars := make([]uint16, length/2)
		for j := range chars {
			chars[j] = binary.LittleEndian.Uint16(encoded[j*2:])
		}
		name := filepath.Base(string(utf16.Decode(chars)))
		modules = append(modules, module{u64(entries, at), u32(entries, at+8), name})
	}
	return modules, nil
}

func owningModule(modules []module, address uint64) (string, uint64, bool) {
	for _, mod := range modules {
		if address >= mod.base && address-mod.base < uint64(mod.size) {
			return mod.name, address - mod.base, true
		}
	}
	return "", 0, false
}

func readThreadStack(r io.ReaderAt, fileSize int64, stream section, threadID uint32) ([]byte, error) {
	if stream.size < 4 {
		return nil, errors.New("no thread list")
	}
	header, err := readAt(r, fileSize, stream.offset, 4)
	if err != nil {
		return nil, err
	}
	count := u32(header, 0)
	if count > 4096 || uint64(count)*48+4 > stream.size {
		return nil, errors.New("invalid thread list size")
	}
	entries, err := readAt(r, fileSize, stream.offset+4, uint64(count)*48)
	if err != nil {
		return nil, err
	}
	for i := uint32(0); i < count; i++ {
		at := int(i) * 48
		if u32(entries, at) != threadID {
			continue
		}
		size := u32(entries, at+32)
		if size > maxStackBytes {
			size = maxStackBytes
		}
		return readAt(r, fileSize, uint64(u32(entries, at+36)), uint64(size))
	}
	return nil, errors.New("faulting thread not present")
}
