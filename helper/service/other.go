//go:build !windows

package service

import (
	"errors"
	"syscall"
)

var errWindowsOnly = errors.New("Windows only")

// detachAttr: creation flags exist only on Windows.
func detachAttr(uint32) *syscall.SysProcAttr { return nil }

func setRunValueOS(string) error   { return errWindowsOnly }
func deleteRunValueOS() error      { return errWindowsOnly }
func listProcsOS() ([]proc, error) { return nil, errWindowsOnly }
func killPidOS(int) error          { return errWindowsOnly }
