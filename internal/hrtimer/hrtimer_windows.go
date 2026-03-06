//go:build windows

package hrtimer

import "syscall"

func init() {
	winmm := syscall.NewLazyDLL("winmm.dll")
	timeBeginPeriod := winmm.NewProc("timeBeginPeriod")
	timeBeginPeriod.Call(1)
}
