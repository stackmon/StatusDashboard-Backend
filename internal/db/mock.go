package db

import (
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"

	"github.com/stackmon/otc-status-dashboard/ent"
)

func NewWithMock() (*DB, sqlmock.Sqlmock, error) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}

	e := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, mockDB)))
	return &DB{sql: mockDB, e: e}, mock, nil
}
