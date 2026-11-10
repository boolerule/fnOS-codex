//go:build !noterminal && !unix

package main

import "os"

func stopTerminalProcess(process *os.Process) {
	_ = process.Kill()
}
