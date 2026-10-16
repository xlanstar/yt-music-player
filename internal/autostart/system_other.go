//go:build !darwin && !linux && !windows

package autostart

import "errors"

func current() (item, error) { return nil, errors.ErrUnsupported }
