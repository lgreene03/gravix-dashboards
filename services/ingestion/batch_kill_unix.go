// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import "syscall"

// syscallKillSelf sends SIGKILL to this process. Used only by
// TestDurabilityUnderKill's child, which must die without running any deferred
// close or flush — the point is to observe what fsync alone left on disk.
func syscallKillSelf() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGKILL)
}
