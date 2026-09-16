// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Machine records the hardware a result was produced on. A benchmark number
// without its machine spec is not comparable to anything.
type Machine struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	NumCPU        int    `json:"num_cpu"`
	CPUModel      string `json:"cpu_model"`
	MemTotalBytes int64  `json:"mem_total_bytes"`
	DiskType      string `json:"disk_type"` // "ssd" | "hdd" | "unknown"
	GoVersion     string `json:"go_version"`
	GravixCommit  string `json:"gravix_commit"`
	Container     bool   `json:"container"`
}

// unknown is the value every field takes when it cannot be determined. Writing
// it out is the whole contract: a guessed CPU model or an assumed SSD makes a
// result look comparable to one measured on real hardware, and the reader has
// no way to tell the difference afterwards.
const unknown = "unknown"

// DetectMachine captures the current machine. Fields it cannot determine are
// set to "unknown" rather than guessed.
func DetectMachine() Machine {
	m := Machine{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		NumCPU:        runtime.NumCPU(),
		CPUModel:      unknown,
		MemTotalBytes: 0,
		DiskType:      unknown,
		GoVersion:     runtime.Version(),
		GravixCommit:  unknown,
		Container:     detectContainer(),
	}
	if model := detectCPUModel(); model != "" {
		m.CPUModel = model
	}
	if mem := detectMemTotal(); mem > 0 {
		m.MemTotalBytes = mem
	}
	if disk := detectDiskType(); disk != "" {
		m.DiskType = disk
	}
	if commit := detectCommit(); commit != "" {
		m.GravixCommit = commit
	}
	return m
}

// detectCPUModel reads /proc/cpuinfo on Linux. Returns "" everywhere else
// rather than inventing a plausible string.
func detectCPUModel() string { return detectCPUModelFrom("/proc/cpuinfo") }

// detectCPUModelFrom is the path-taking form, so a test can point it at a
// machine with no /proc and check it declines rather than guesses.
func detectCPUModelFrom(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "Model": // x86 and arm spell it differently
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// detectMemTotal reads MemTotal from /proc/meminfo, in bytes.
func detectMemTotal() int64 { return detectMemTotalFrom("/proc/meminfo") }

// detectMemTotalFrom is the path-taking form; see detectCPUModelFrom.
func detectMemTotalFrom(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "MemTotal:" {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// detectDiskType reads the rotational flag the kernel exposes per block device.
// 0 means non-rotational, which is what "ssd" means here; 1 means a spinning
// disk. Anything else — a virtual device, a container without /sys, a
// filesystem spanning several devices — stays unknown.
func detectDiskType() string { return detectDiskTypeIn("/sys/block") }

// detectDiskTypeIn is the root-taking form, so the mixed-device and
// virtual-device rules can be tested against fixtures rather than against
// whatever the build machine happens to have.
func detectDiskTypeIn(root string) string {
	entries, err := filepath.Glob(filepath.Join(root, "*", "queue", "rotational"))
	if err != nil || len(entries) == 0 {
		return ""
	}
	sawSSD, sawHDD := false, false
	for _, path := range entries {
		// Loop and ram devices are not the storage the benchmark runs on.
		dev := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if strings.HasPrefix(dev, "loop") || strings.HasPrefix(dev, "ram") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(data)) {
		case "0":
			sawSSD = true
		case "1":
			sawHDD = true
		}
	}
	// A mix is genuinely unknown: nothing here says which one holds the
	// working directory.
	switch {
	case sawSSD && !sawHDD:
		return "ssd"
	case sawHDD && !sawSSD:
		return "hdd"
	default:
		return ""
	}
}

// detectCommit asks git. A dirty tree is reported as such, because a result
// produced from uncommitted changes cannot be reproduced from the commit alone.
func detectCommit() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return ""
	}
	if status, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		if len(strings.TrimSpace(string(status))) > 0 {
			return commit + "-dirty"
		}
	}
	return commit
}

// detectContainer reports whether this looks like a container, which matters
// because a CPU quota makes NumCPU a lie about available parallelism.
func detectContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// cgroup v2 exposes a quota here; a non-"max" value means the process is
	// capped below what runtime.NumCPU reports.
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 && fields[0] != "max" {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		body := string(data)
		if strings.Contains(body, "docker") || strings.Contains(body, "kubepods") ||
			strings.Contains(body, "containerd") {
			return true
		}
	}
	return false
}
