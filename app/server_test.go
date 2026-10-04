package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServerRefusesToStartWithoutADatabase(t *testing.T) {
	c := testConfig()
	// Port 1 is reserved and nothing listens on it, so the dial is refused
	// instead of hanging.
	c.DB = DBConfig{
		Host:     "127.0.0.1",
		Port:     "1",
		User:     "postgres",
		Password: "postgres",
		Name:     "boilerplate",
		SSLMode:  "disable",
	}

	// gorm.Open does not wait for postgres, so without the startup ping this
	// would report success and only fail on the first request that touched a
	// table.
	_, err := newServer(t.Context(), c)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres")
}

func TestOpenDatabaseRejectsAnIncompleteAuthConfig(t *testing.T) {
	c := testConfig()
	c.Auth.AccessSecret = ""

	s := &server{c: c}

	err := s.openDatabase(t.Context())

	// Validated before any dial, so a misconfigured secret never turns into a
	// confusing authentication failure at request time.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth configuration")
	assert.Nil(t, s.db)
}
