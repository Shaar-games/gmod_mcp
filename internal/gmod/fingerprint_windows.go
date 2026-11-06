//go:build windows && 386

package gmod

import (
	"fmt"
	"golang.org/x/sys/windows"
)

func (g *GMod) BinaryFingerprints() []FileFingerprint {
	var results []FileFingerprint
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(g.handle, 0, &buf[0], &size); err == nil {
		results = append(results, FingerprintFile(windows.UTF16ToString(buf[:size])))
	} else {
		results = append(results, FileFingerprint{Path: "gmod.exe", Error: err.Error()})
	}
	for _, name := range []string{"client.dll", "engine.dll", "materialsystem.dll", "resources.dll"} {
		info, err := findModuleInfo(g.pid, name)
		if err != nil {
			continue
		} // optional modules may not be loaded yet
		if info.Path == "" {
			results = append(results, FileFingerprint{Path: name, Error: fmt.Sprintf("module path unavailable for PID %d", g.pid)})
		} else {
			results = append(results, FingerprintFile(info.Path))
		}
	}
	return results
}
