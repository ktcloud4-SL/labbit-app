package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// rollbackTimeout은 rollback이 요청 context 취소와 무관하게 끝날 수 있는 시간이다.
const rollbackTimeout = 5 * time.Second

// dbtx는 Pool과 Tx가 공통으로 제공하는 query 실행 경계다.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// queries는 하나의 dbtx 위에서 repository.Repositories를 구현한다.
// transaction 안에서는 pgx.Tx, 그 밖에서는 Pool을 사용한다.
type queries struct {
	db dbtx
}

// Store는 repository.Repositories와 repository.Transactor의 PostgreSQL 구현이다.
// Repositories 메서드를 직접 호출하면 Pool에서 각각 독립된 statement로 실행한다.
type Store struct {
	queries
	pool *pgxpool.Pool
}

var (
	_ repository.Repositories        = (*Store)(nil)
	_ repository.Transactor          = (*Store)(nil)
	_ repository.ConnectorRepository = (*Store)(nil)
)

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{queries: queries{db: pool}, pool: pool}
}

// WithinTransaction은 READ COMMITTED transaction 하나에서 fn을 실행한다.
// 계약은 repository.Transactor를 따른다.
func (s *Store) WithinTransaction(ctx context.Context, fn func(ctx context.Context, repos repository.Repositories) error) error {
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error { return fn(ctx, q) })
}

// inTransaction은 WithinTransaction과 같은 규칙으로 transaction을 열고 그 transaction에 묶인 queries를 fn에 전달한다.
// Repositories에 속하지 않는 adapter 내부 query(예: 여러 row를 순서대로 lock해야 하는 단일 use case)가 사용한다.
func (s *Store) inTransaction(ctx context.Context, fn func(ctx context.Context, q queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return normalize("BeginTransaction", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// fn 오류, panic, commit 실패 모두 rollback한다. 요청 context가 이미 취소되었어도
		// 열린 transaction이 connection과 함께 pool로 돌아가지 않도록 별도 deadline을 사용한다.
		// rollback이 실패하면 pgx가 connection을 닫으므로 오류는 무시한다.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	// 중첩 transaction을 시작할 수 없도록 Transactor를 제공하지 않는 queries만 전달한다.
	if err := fn(ctx, queries{db: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		// fn이 DB 오류를 삼키고 nil을 반환한 경우 PostgreSQL은 aborted transaction의 COMMIT을
		// ROLLBACK으로 처리한다. pgx가 이를 오류로 반환하므로 성공으로 취급하지 않는다.
		return normalize("CommitTransaction", err)
	}
	committed = true
	return nil
}
