//go:build !linux

package openstackprovider

import "os"

func recoveryOwnedPath(string, os.FileMode, bool) bool { return false }
