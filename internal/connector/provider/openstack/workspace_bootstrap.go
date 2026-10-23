package openstackprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

//go:embed workspace_bootstrap.py
var workspaceBootstrapScript string

var errWorkspaceBootstrap = errors.New("Workspace preparation failed")

// An omitted source preserves existing image/manual preparation deployments.
// Official installs always configure source + digest and enable this preflight.
func workspaceBootstrapArtifact(config ProvisionConfig) ([]byte, error) {
	if config.WorkspaceHelperSource == "" && config.WorkspaceHelperSHA256 == "" {
		return nil, nil
	}
	validVMPath := func(p string) bool {
		return p != "/" && strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n\\")
	}
	if !validVMPath(config.WorkspaceRoot) || !validVMPath(config.WorkspaceHelper) || config.WorkspaceRoot == config.WorkspaceHelper || strings.HasPrefix(config.WorkspaceHelper, config.WorkspaceRoot+"/") || strings.HasPrefix(config.WorkspaceRoot, config.WorkspaceHelper+"/") || !filepath.IsAbs(config.WorkspaceHelperSource) || filepath.Clean(config.WorkspaceHelperSource) != config.WorkspaceHelperSource {
		return nil, errWorkspaceBootstrap
	}
	expected, err := hex.DecodeString(config.WorkspaceHelperSHA256)
	if err != nil || len(expected) != sha256.Size {
		return nil, errWorkspaceBootstrap
	}
	info, err := os.Lstat(config.WorkspaceHelperSource)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() > 32<<20 {
		return nil, errWorkspaceBootstrap
	}
	file, err := os.Open(config.WorkspaceHelperSource)
	if err != nil {
		return nil, errWorkspaceBootstrap
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errWorkspaceBootstrap
	}
	payload, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	digest := sha256.Sum256(payload)
	if err != nil || len(payload) > 32<<20 || !bytes.Equal(digest[:], expected) {
		return nil, errWorkspaceBootstrap
	}
	image, err := elf.NewFile(bytes.NewReader(payload))
	if err != nil {
		return nil, errWorkspaceBootstrap
	}
	defer image.Close()
	if image.Class != elf.ELFCLASS64 || image.Machine != elf.EM_X86_64 || image.Type != elf.ET_EXEC || image.Data != elf.ELFDATA2LSB {
		return nil, errWorkspaceBootstrap
	}
	return payload, nil
}

func shellArgument(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (a *Adapter) prepareWorkspace(ctx context.Context, request coreprovider.ProvisionRequest, vmKey, serverID string, artifact []byte) error {
	if len(artifact) == 0 || vmKey != request.CreationSnapshot.WorkspaceVMKey {
		return nil
	}
	target := vmaccess.Target{LabInstanceID: request.LabInstanceID, Generation: request.Generation, VMKey: vmKey, ServerID: serverID}
	if err := a.ValidateWorkspace(ctx, target); err != nil {
		return errWorkspaceBootstrap
	}
	endpoint, err := a.ResolveVMAccess(ctx, target)
	if err != nil {
		return errWorkspaceBootstrap
	}
	hostIdentity, err := sshHostKeyIdentity(a.provision.ProviderConnectionID, serverID)
	if err != nil {
		return errWorkspaceBootstrap
	}
	config := a.provision
	command := "sudo -n python3 -c " + shellArgument(workspaceBootstrapScript)
	for _, value := range []string{config.WorkspaceRoot, config.WorkspaceHelper, config.SSHUsername, strings.ToLower(config.WorkspaceHelperSHA256)} {
		command += " " + shellArgument(value)
	}
	bootstrapCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := a.runSSHCommandInput(bootstrapCtx, endpoint.Address, hostIdentity, command, bytes.NewReader(artifact), endpoint.HostKeyCallback); err != nil {
		return errors.Join(errWorkspaceBootstrap, err)
	}
	return nil
}
