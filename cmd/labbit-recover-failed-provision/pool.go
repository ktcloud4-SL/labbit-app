package main

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
)

func recoveryPool(ctx context.Context, dsn, tunnel string) (*pgxpool.Pool, error) {
	config, err := recoveryPoolConfig(dsn, tunnel)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, config)
}

func recoveryPoolConfig(dsn, tunnel string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, operationrecovery.ErrUnsafe
	}
	// Operator recovery requires authenticated TLS even through a local tunnel.
	// Changing only DialFunc leaves CA/SNI/hostname verification intact.
	tls := config.ConnConfig.TLSConfig
	if tls == nil || tls.InsecureSkipVerify || tls.ServerName == "" {
		return nil, operationrecovery.ErrUnsafe
	}
	if tunnel != "" {
		host, portText, err := net.SplitHostPort(tunnel)
		if err != nil {
			return nil, operationrecovery.ErrUnsafe
		}
		ip := net.ParseIP(host)
		port, err := strconv.Atoi(portText)
		if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
			return nil, operationrecovery.ErrUnsafe
		}
		config.ConnConfig.LookupFunc = func(context.Context, string) ([]string, error) { return []string{config.ConnConfig.Host}, nil }
		config.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != net.JoinHostPort(config.ConnConfig.Host, strconv.Itoa(int(config.ConnConfig.Port))) {
				return nil, operationrecovery.ErrUnsafe
			}
			return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", tunnel)
		}
	}
	return config, nil
}
