package db

import (
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/ent"
)

func NewWithMock() (*DB, sqlmock.Sqlmock, error) {
	mockDB, mock, _ := sqlmock.New()
	dialector := postgres.New(postgres.Config{
		Conn:       mockDB,
		DriverName: "postgres",
	})

	g, _ := gorm.Open(dialector, &gorm.Config{})
	e := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, mockDB)))
	return &DB{g: g, e: e}, mock, nil
}
