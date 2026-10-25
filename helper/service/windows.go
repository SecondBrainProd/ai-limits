//go:build windows

package service

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// detachAttr applies Windows creation flags to the started process.
func detachAttr(flags uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: flags}
}

func setRunValueOS(value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("cannot open the Run key: %v", err)
	}
	defer k.Close()
	if err := k.SetStringValue(runValue, value); err != nil {
		return fmt.Errorf("cannot write the Run key: %v", err)
	}
	return nil
}

func deleteRunValueOS() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func listProcsOS() ([]proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var procs []proc
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		procs = append(procs, proc{int(e.ProcessID), windows.UTF16ToString(e.ExeFile[:])})
	}
	return procs, nil
}

func killPidOS(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
