//go:build !linux && !android

package scrcpy

import "syscall"

// The on-device (root, no adb) transport only exists on Linux/Android: it relies
// on app_process, /data/local/tmp and unix abstract sockets. On other platforms
// these helpers allow host-side compilation and static checks only.

func localServerGroups() []uint32 { return nil }

func localSysProcAttr(groups []uint32) *syscall.SysProcAttr { return nil }
