package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
)

// TxRunner runs fn with a Querier bound to one transaction.
type TxRunner interface {
	InTx(ctx context.Context, fn func(q sqlc.Querier) error) error
}

type poolTx struct{ pool *pgxpool.Pool }

func PoolTx(pool *pgxpool.Pool) TxRunner { return poolTx{pool} }

func (p poolTx) InTx(ctx context.Context, fn func(q sqlc.Querier) error) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

type sameTx struct{ q sqlc.Querier }

// SameTx runs fn directly on q. Use it when q is already bound to a transaction.
func SameTx(q sqlc.Querier) TxRunner { return sameTx{q} }

func (s sameTx) InTx(_ context.Context, fn func(q sqlc.Querier) error) error { return fn(s.q) }
