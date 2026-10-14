package workspacefs

import (
	"errors"
	"os"
)

// Windows supports in-process tests only. The installed helper runs on Linux.
const safeOpenFlags = 0

func lockFile(*os.File) error                   { return nil }
func ValidateInstallation(string, string) error { return errors.New("unsupported workspace platform") }

func openRoot(directory string) (*os.Root, error) { return os.OpenRoot(directory) }
