package db

import (
	"context"
	"database/sql"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent"
)

// Tx wraps the underlying database transaction. Callers pass it through to the
// *Tx facade methods and PublishTx; they never call methods on it directly.
type Tx struct {
	client *ent.Client
	driver *sharedTxDriver
	sqlTx  *sql.Tx
}

// sharedTxDriver keeps Ent's inner transactions on the connection that already
// started the outer one: edge inserts open a "transaction" of their own, and a
// second database transaction from inside a *sql.Tx is neither possible nor wanted.
type sharedTxDriver struct {
	*entsql.Driver
}

func (d *sharedTxDriver) Tx(context.Context) (dialect.Tx, error) {
	return dialect.NopTx(d), nil
}

// clientFor returns the Ent client bound to tx, or the pool client when tx is nil.
func (db *DB) clientFor(tx *Tx) *ent.Client {
	if tx != nil && tx.client != nil {
		return tx.client
	}
	return db.e
}

// rawFor returns the raw SQL executor scoped to tx, or the pool when tx is nil.
// It keeps hand-written statements (row locks, aggregates) on the same connection
// as the surrounding Ent transaction.
func (db *DB) rawFor(tx *Tx) entsql.ExecQuerier {
	if tx != nil && tx.driver != nil {
		return tx.driver
	}
	return db.sql
}

// begin starts a transaction whose Ent client and raw executor share one connection.
func (db *DB) begin(ctx context.Context) (*Tx, error) {
	sqlTx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	driver := &sharedTxDriver{
		Driver: entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: sqlTx}),
	}
	return &Tx{
		client: ent.NewClient(ent.Driver(driver)),
		driver: driver,
		sqlTx:  sqlTx,
	}, nil
}

func (tx *Tx) commit() error {
	return tx.sqlTx.Commit()
}

func (tx *Tx) rollback() error {
	return tx.sqlTx.Rollback()
}

// execWithTx runs fn in a transaction when tx is nil; otherwise it runs on the
// caller's transaction. fn receives the Ent client and the raw executor bound to
// the same transaction.
func (db *DB) execWithTx(
	ctx context.Context, tx *Tx, fn func(client *ent.Client, raw entsql.ExecQuerier) error,
) error {
	if tx != nil {
		return fn(tx.client, tx.driver)
	}

	t, err := db.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = t.rollback() }()

	if err = fn(t.client, t.driver); err != nil {
		return err
	}
	return t.commit()
}
