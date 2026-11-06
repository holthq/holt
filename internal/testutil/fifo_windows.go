//go:build windows

package testutil

import "os"

// MakeFIFO 在 Windows 上没有对应的东西，总是返回 ErrNoFIFO。
func MakeFIFO(string, os.FileMode) error { return ErrNoFIFO }
