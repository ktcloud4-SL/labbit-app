package previewtest

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func connectionRefused(err error) bool {
	// Winsock errors are distinct from the portable syscall errno constants.
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}
