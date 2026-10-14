// labbit-workspace-sftp runs as the VM's management user on a private SSH channel.
// It has no network listener and logs no paths, directory entries or file bytes.
package main

import (
	"flag"
	"io"
	"os"
	"runtime"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefs"
)

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return nil }
func main() {
	root := flag.String("root", "", "administrator-configured Workspace root")
	limit := flag.Int64("max-bytes", 1<<20, "bounded file comparison size")
	flag.Parse()
	if runtime.GOOS != "linux" || flag.NArg() != 0 || *root == "" {
		os.Exit(2)
	}
	executable, err := os.Executable()
	if err != nil || workspacefs.ValidateInstallation(*root, executable) != nil {
		os.Exit(2)
	}
	if err := workspacefs.ServeWithLimit(stdio{}, *root, *limit); err != nil && err != io.EOF {
		os.Exit(1)
	}
}
