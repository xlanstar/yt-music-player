//go:build !windows

package autostart

import "errors"

// currentPath fails: only Windows' installer leaves ytm off the PATH.
func currentPath() (userPath, error) { return userPath{}, errors.ErrUnsupported }
