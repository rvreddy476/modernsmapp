//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// readNoEcho switches echo off with stty for the read. When stdin is not a
// terminal stty fails and the line is read plainly (scripted runs).
func readNoEcho(f *os.File) (string, error) {
	off := exec.Command("stty", "-echo")
	off.Stdin = f
	if err := off.Run(); err != nil {
		return readLine(f)
	}
	defer func() {
		on := exec.Command("stty", "echo")
		on.Stdin = f
		_ = on.Run()
	}()
	return readLine(f)
}
