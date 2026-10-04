// Package repo holds the generic persistence port and its generic GORM
// implementation. Domain specific queries live in the package that owns the
// domain, on top of GormRepository, so that neither the domain nor this
// package has to know about the other's entities.
package repo

import (
	"context"
	"errors"
)

// ErrNotFound is returned by every lookup that matches no record. Adapters
// translate driver specific errors into this sentinel so callers can check it
// with errors.Is without importing the driver.
var ErrNotFound = errors.New("repo: record not found")

// ErrDuplicate is returned when an insert or update collides with a unique
// index. Domains translate it into whatever "already taken" means to them, so
// callers never have to recognise the driver's error.
var ErrDuplicate = errors.New("repo: unique constraint violated")

// Repository is the generic CRUD contract implemented by GormRepository.
// Lookups report ErrNotFound when nothing matches.
type Repository[T any] interface {
	Create(ctx context.Context, entity *T) error
	GetByID(ctx context.Context, id uint) (*T, error)
	Update(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id uint) error
	FindAll(ctx context.Context) ([]T, error)
}
