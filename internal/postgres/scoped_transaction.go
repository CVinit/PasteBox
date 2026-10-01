package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Coordinated content operations must use their existing transaction rather
// than borrow another pool connection (which can deadlock a saturated pool).
type scopedTransactionKey struct{}
type scopedTransaction struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

type contentQueryer interface {
	execQuerier
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func contentDB(ctx context.Context, pool *pgxpool.Pool) contentQueryer {
	if scoped, ok := ctx.Value(scopedTransactionKey{}).(scopedTransaction); ok && scoped.pool == pool {
		return scoped.tx
	}
	return pool
}
