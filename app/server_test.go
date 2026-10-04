package app

import (
	"net/http"
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

func TestNewServerRefusesTLSWithoutCertificates(t *testing.T) {
	c := testConfig()
	c.Server.Env = "production"

	_, err := newServer(t.Context(), c)

	// Checked before the database is opened, so the operator sees the missing
	// certificates rather than a postgres dial.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cert_file")
}

func TestServerServesTLSWhenTheEnvironmentAsksForIt(t *testing.T) {
	c := testConfig()
	c.Server.Env = "production"
	c.Server.CertFile = "testdata/missing-cert.pem"
	c.Server.KeyFile = "testdata/missing-key.pem"

	certFile, keyFile, err := c.TLSFiles()
	require.NoError(t, err)

	s := &server{c: c, tlsCertFile: certFile, tlsKeyFile: keyFile}
	// Port 0 is bound by the kernel and released as soon as this returns, so
	// nothing clashes with a real listener.
	s.httpServer = http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}

	// Only ListenAndServeTLS ever reads the certificate, so an error naming the
	// missing file is how this pins the TLS path without a handshake.
	err = s.serve()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-cert.pem")
}
