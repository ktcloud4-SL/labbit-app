package drcontrol

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseConfig performs no I/O. DSNs come from a protected file, not argv.
// No fallback hosts, unverified TLS, multi-host DSN, or injected runtime options.
func DatabaseConfig(p Policy, dsn string) (*pgxpool.Config, error) {
	if p.Validate() != nil {
		return nil, ErrInput
	}
	c, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, ErrInput
	}
	d := p.Database
	v := c.ConnConfig
	if v.Host != d.Host || v.Port != d.Port || v.Database != d.Name || v.User != d.User || len(v.Fallbacks) != 0 || v.TLSConfig == nil || v.TLSConfig.InsecureSkipVerify || v.TLSConfig.ServerName != d.Host {
		return nil, ErrDenied
	}
	if len(v.RuntimeParams) > 0 {
		return nil, ErrDenied
	}
	v.TLSConfig.MinVersion = tls.VersionTLS12
	v.ConnectTimeout = 2 * time.Second
	v.RuntimeParams = map[string]string{"application_name": "labbit-dr-control", "search_path": "pg_catalog,public"}
	c.MaxConns = 1
	c.MinConns = 0
	return c, nil
}
func VerifySQL(ctx context.Context, p *pgxpool.Pool, d Database) error {
	if p == nil {
		return ErrUnavailable
	}
	var recovery bool
	var db, user string
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if p.QueryRow(c, "SELECT pg_is_in_recovery(), current_database(), session_user").Scan(&recovery, &db, &user) != nil {
		return ErrUnavailable
	}
	if recovery || db != d.Name || user != d.User {
		return ErrDenied
	}
	return nil
}
