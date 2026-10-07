package wss_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestConfig_GetCredentialRejectsEmptySecret(t *testing.T) {
	if _, err := (&wss.Config{Credential: "   "}).GetCredential(); err == nil {
		t.Fatal("whitespace-only direct credential was accepted")
	}
	path := filepath.Join(t.TempDir(), "connector-credential")
	if err := os.WriteFile(path, []byte("\n\t"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&wss.Config{CredentialFile: path}).GetCredential(); err == nil {
		t.Fatal("empty credential file was accepted")
	}
}
