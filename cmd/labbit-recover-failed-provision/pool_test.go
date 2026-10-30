package main

import "testing"

func TestRecoveryPoolTunnelPreservesTLSAndRejectsExternalTargets(t *testing.T) {
	dsn := "postgres://fixture:dummy@db.example.test:5432/postgres?sslmode=verify-full"
	for _, tunnel := range []string{"", "127.0.0.1:15432", "[::1]:15432"} {
		config, err := recoveryPoolConfig(dsn, tunnel)
		if err != nil {
			t.Fatal(err)
		}
		if config.ConnConfig.TLSConfig.ServerName != "db.example.test" || config.ConnConfig.TLSConfig.InsecureSkipVerify || config.ConnConfig.Host != "db.example.test" {
			t.Fatal("tunnel weakened original TLS identity")
		}
	}
	for _, tunnel := range []string{"remote.example:5432", "10.0.0.1:5432", "127.0.0.1:0", "127.0.0.1:65536", "localhost:5432"} {
		if _, err := recoveryPoolConfig(dsn, tunnel); err == nil {
			t.Fatalf("unsafe tunnel accepted %s", tunnel)
		}
	}
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca"} {
		if _, err := recoveryPoolConfig("postgres://fixture:dummy@db.example.test/postgres?sslmode="+mode, ""); err == nil {
			t.Fatalf("unauthenticated DB TLS mode accepted %s", mode)
		}
	}
}
