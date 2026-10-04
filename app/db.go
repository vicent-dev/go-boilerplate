package app

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"go-boilerplate/pkg/auth"
)

// dbPingTimeout bounds the startup database check. It is short because it only
// has to answer "is postgres there", not serve traffic: an api that cannot reach
// its database has nothing useful to do, and the supervisor restarts it.
const dbPingTimeout = 10 * time.Second

// OpenDB dials postgres, sizes the connection pool and migrates the tables the
// bundled domains own. It is exported so that every command reuses one
// connection setup instead of repeating it.
//
// Every error is returned rather than panicked on: a worker and an admin
// command both need to fail cleanly when the database is not up yet, which is
// the normal state during a compose start.
func OpenDB(cfg DBConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdle)

	if err := auth.Migrate(db); err != nil {
		return nil, fmt.Errorf("migrate auth tables: %w", err)
	}

	return db, nil
}

// CloseDB releases the connection pool.
func CloseDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// pingDB reports whether the database answers, so a caller can turn a
// connection problem into a clear message instead of a failed first query.
func pingDB(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
