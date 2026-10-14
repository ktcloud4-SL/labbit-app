//go:build !linux && !windows

package workspacefs

import (
	"errors"
	"os"
)

const safeOpenFlags = 0

func lockFile(*os.File) error                   { return errors.New("unsupported workspace platform") }
func ValidateInstallation(string, string) error { return errors.New("unsupported workspace platform") }

func openRoot(string) (*os.Root, error) { return nil, errors.New("unsupported workspace platform") }
