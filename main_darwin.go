//go:build darwin

package main

import "os/exec"

func openFullDiskAccessSettings() {
	exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles").Run()
}

func fullDiskAccessHint() string {
	return `    open "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"  (add your terminal, then re-run)`
}
