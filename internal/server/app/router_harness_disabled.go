//go:build !backend_router_harness

package app

import (
	"context"
	"log/slog"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// 기본 artifact는 환경변수/파일을 읽거나 goroutine을 시작하지 않는다.
type routerHarness struct{}

func newRouterHarness() *routerHarness                                           { return &routerHarness{} }
func (*routerHarness) sink() connector.EventSink                                 { return nil }
func (*routerHarness) start(context.Context, *controlStack, *slog.Logger) func() { return func() {} }
