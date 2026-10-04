package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// GormRepository implements Repository[T] on top of gorm.
type GormRepository[T any] struct {
	db *gorm.DB
}

// NewGormRepository returns a repository bound to db.
func NewGormRepository[T any](db *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: db}
}

// WithTx returns a copy bound to the given transaction.
func (r *GormRepository[T]) WithTx(tx *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: tx}
}

// DB exposes the underlying handle so that a repository living next to its
// domain can run its own queries, instead of forcing every query through the
// generic string based API below.
func (r *GormRepository[T]) DB() *gorm.DB {
	return r.db
}

// Create inserts entity, filling in generated fields such as the primary key. A
// unique index collision is reported as ErrDuplicate.
func (r *GormRepository[T]) Create(ctx context.Context, entity *T) error {
	return NormalizeError(r.db.WithContext(ctx).Create(entity).Error)
}

// GetByID returns the entity with the given primary key, or ErrNotFound.
func (r *GormRepository[T]) GetByID(ctx context.Context, id uint) (*T, error) {
	var entity T
	if err := r.db.WithContext(ctx).First(&entity, id).Error; err != nil {
		return nil, NormalizeError(err)
	}
	return &entity, nil
}

// Update persists every field of entity. A unique index collision is reported
// as ErrDuplicate.
func (r *GormRepository[T]) Update(ctx context.Context, entity *T) error {
	return NormalizeError(r.db.WithContext(ctx).Save(entity).Error)
}

// Delete removes the entity with the given primary key, honouring soft deletes
// for models that declare gorm.DeletedAt.
func (r *GormRepository[T]) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(new(T), id).Error
}

// FindAll returns every entity.
func (r *GormRepository[T]) FindAll(ctx context.Context) ([]T, error) {
	entities := make([]T, 0)
	if err := r.db.WithContext(ctx).Find(&entities).Error; err != nil {
		return nil, NormalizeError(err)
	}
	return entities, nil
}

// uniqueViolation matches what postgres and sqlite both report for a unique
// index collision. Matching on the message is unpleasant, but it is the only
// portable signal: gorm surfaces the raw driver error and neither database
// offers a typed error for it.
var uniqueViolation = "UNIQUE constraint failed"

// NormalizeError maps driver specific errors to the sentinels of this package
// and leaves every other error untouched.
func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	// sqlite is case insensitive about constraint names, postgres is not.
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(uniqueViolation)) {
		return fmt.Errorf("%w: %w", ErrDuplicate, err)
	}
	return err
}
