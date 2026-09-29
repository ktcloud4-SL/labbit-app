package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fileDSN = "postgres://file-user:file-dummy-secret@db.internal:5432/labbit"
	envDSN  = "postgres://env-user:env-dummy-secret@localhost:5432/labbit"
)

func writeDSNFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dsn")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDSN(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		fileContent *string
		envValue    string
		want        string
		wantErr     string
	}{
		{
			name:        "file form wins over env form",
			environment: "development",
			fileContent: ptr(fileDSN + "\n"),
			envValue:    envDSN,
			want:        fileDSN,
		},
		{
			name:        "production uses file form",
			environment: "production",
			fileContent: ptr(fileDSN),
			envValue:    envDSN,
			want:        fileDSN,
		},
		{
			name:        "development env escape hatch",
			environment: "development",
			envValue:    envDSN,
			want:        envDSN,
		},
		{
			name:        "production rejects env-only DSN",
			environment: "production",
			envValue:    envDSN,
			wantErr:     "production에서는 LABBIT_DATABASE_DSN_FILE이 필요합니다",
		},
		{
			name:        "empty file",
			environment: "development",
			fileContent: ptr(" \n"),
			envValue:    envDSN,
			wantErr:     "비어 있습니다",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
			if tt.fileContent != nil {
				t.Setenv("LABBIT_DATABASE_DSN_FILE", writeDSNFile(t, *tt.fileContent))
			}
			t.Setenv("LABBIT_DATABASE_DSN", tt.envValue)

			got, err := LoadDSN(tt.environment)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadDSN() error = %v, want containing %q", err, tt.wantErr)
				}
				assertNoSecret(t, err)
				return
			}
			if err != nil {
				t.Fatalf("LoadDSN() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("LoadDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadDSNNotConfigured(t *testing.T) {
	t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
	t.Setenv("LABBIT_DATABASE_DSN", "")

	if _, err := LoadDSN("development"); !errors.Is(err, ErrDSNNotConfigured) {
		t.Fatalf("LoadDSN() error = %v, want ErrDSNNotConfigured", err)
	}
}

func TestLoadDSNMissingFileDoesNotFallBackToEnv(t *testing.T) {
	t.Setenv("LABBIT_DATABASE_DSN_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("LABBIT_DATABASE_DSN", envDSN)

	_, err := LoadDSN("development")
	if err == nil || !strings.Contains(err.Error(), "LABBIT_DATABASE_DSN_FILE을 읽을 수 없습니다") {
		t.Fatalf("LoadDSN() error = %v, want file read error", err)
	}
	assertNoSecret(t, err)
}

func TestInvalidDSNErrorDoesNotExposeValue(t *testing.T) {
	invalid := "postgres://user:invalid-dummy-secret@localhost:not-a-port/labbit"

	_, poolErr := OpenPool(context.Background(), invalid)
	_, connErr := Connect(context.Background(), invalid)
	for _, err := range []error{poolErr, connErr} {
		if !errors.Is(err, errInvalidDSN) {
			t.Fatalf("error = %v, want errInvalidDSN", err)
		}
		if strings.Contains(err.Error(), "invalid-dummy-secret") || strings.Contains(err.Error(), "localhost") {
			t.Fatalf("error exposes DSN: %v", err)
		}
	}
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	for _, secret := range []string{"file-dummy-secret", "env-dummy-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error exposes DSN secret: %v", err)
		}
	}
}

func ptr(value string) *string {
	return &value
}
