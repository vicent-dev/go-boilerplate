package repo

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type widget struct {
	ID    uint   `gorm:"primarykey"`
	Name  string `gorm:"uniqueIndex;not null"`
	Stock int
}

func newTestRepo(t *testing.T) *GormRepository[widget] {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(1)"), &gorm.Config{
		Logger: logger.Discard,
	})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&widget{}))
	require.NoError(t, db.AutoMigrate(&widget{}))

	return NewGormRepository[widget](db)
}

func TestGormRepositoryCreateFillsID(t *testing.T) {
	r := newTestRepo(t)

	w := &widget{Name: "bolt", Stock: 3}
	require.NoError(t, r.Create(context.Background(), w))

	assert.NotZero(t, w.ID)

	stored, err := r.GetByID(context.Background(), w.ID)
	require.NoError(t, err)
	assert.Equal(t, "bolt", stored.Name)
	assert.Equal(t, 3, stored.Stock)
}

func TestGormRepositoryGetByIDNotFound(t *testing.T) {
	r := newTestRepo(t)

	_, err := r.GetByID(context.Background(), 404)

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGormRepositoryUpdate(t *testing.T) {
	r := newTestRepo(t)

	w := &widget{Name: "nut", Stock: 1}
	require.NoError(t, r.Create(context.Background(), w))

	w.Stock = 9
	require.NoError(t, r.Update(context.Background(), w))

	stored, err := r.GetByID(context.Background(), w.ID)
	require.NoError(t, err)
	assert.Equal(t, 9, stored.Stock)
}

func TestGormRepositoryDelete(t *testing.T) {
	r := newTestRepo(t)

	w := &widget{Name: "screw"}
	require.NoError(t, r.Create(context.Background(), w))
	require.NoError(t, r.Delete(context.Background(), w.ID))

	_, err := r.GetByID(context.Background(), w.ID)

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGormRepositoryFindAll(t *testing.T) {
	r := newTestRepo(t)

	require.NoError(t, r.Create(context.Background(), &widget{Name: "a"}))
	require.NoError(t, r.Create(context.Background(), &widget{Name: "b"}))

	all, err := r.FindAll(context.Background())

	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestGormRepositoryFindAllEmptyIsNotNil(t *testing.T) {
	r := newTestRepo(t)

	all, err := r.FindAll(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, all)
	assert.Empty(t, all)
}

func TestGormRepositoryWithTxRollsBack(t *testing.T) {
	r := newTestRepo(t)
	require.NoError(t, r.Create(context.Background(), &widget{Name: "outside"}))

	tx := r.DB().Begin()
	txRepo := r.WithTx(tx)
	require.NoError(t, txRepo.Create(context.Background(), &widget{Name: "inside"}))
	require.NoError(t, tx.Rollback().Error)

	all, err := r.FindAll(context.Background())

	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "outside", all[0].Name)
}

func TestNormalizeError(t *testing.T) {
	assert.Equal(t, ErrNotFound, NormalizeError(gorm.ErrRecordNotFound))

	wrapped := fmt.Errorf("while loading user: %w", gorm.ErrRecordNotFound)
	assert.ErrorIs(t, NormalizeError(wrapped), ErrNotFound)

	other := errors.New("connection refused")
	assert.Equal(t, other, NormalizeError(other))

	assert.NoError(t, NormalizeError(nil))
}

func TestRepositoryInterfaceIsImplemented(t *testing.T) {
	var _ Repository[widget] = (*GormRepository[widget])(nil)
}
