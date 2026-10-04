package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"go-boilerplate/pkg/queue"
)

func TestLoadConfigExpandsEnvironment(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("RABBIT_HOST", "testbroker")

	testConfig := []byte(`
server:
  host: 127.0.0.1
  port: ${PORT}
rabbit:
  host: ${RABBIT_HOST}
  port: 5672
  queues: {}
`)
	c := &Config{}
	require.NoError(t, yaml.Unmarshal([]byte(expandEnv(string(testConfig))), c))
	assert.Equal(t, "9090", c.Server.Port)
	assert.Equal(t, "testbroker", c.Rabbit.Host)
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("SET", "value")
	t.Setenv("EMPTY", "")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain variable", in: "${SET}", want: "value"},
		{name: "unset variable", in: "${UNSET}", want: ""},
		{name: "default when unset", in: "${UNSET:-fallback}", want: "fallback"},
		{name: "default when empty", in: "${EMPTY:-fallback}", want: "fallback"},
		{name: "environment wins over the default", in: "${SET:-fallback}", want: "value"},
		{name: "default may be empty", in: "${UNSET:-}", want: ""},
		{name: "inside a value", in: "host: ${SET}:${UNSET:-5432}", want: "host: value:5432"},
		{name: "a fallback is not expanded again", in: "${UNSET:-$OTHER}", want: "$OTHER"},
		{name: "no variable", in: "plain", want: "plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, expandEnv(tt.in))
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	c := (&Config{}).withDefaults()

	// 0.0.0.0 rather than 127.0.0.1: the default has to work inside a container,
	// where binding to loopback makes the server unreachable.
	assert.Equal(t, "0.0.0.0", c.Server.Host)
	assert.Equal(t, "8080", c.Server.Port)
	// local, so a fresh clone serves plaintext instead of refusing to start on
	// certificate files it does not have.
	assert.Equal(t, "local", c.Server.Env)
	assert.Equal(t, "5432", c.DB.Port)
	assert.Equal(t, 100, c.DB.MaxConns)
	assert.Equal(t, 10, c.DB.MaxIdle)
	assert.Equal(t, "disable", c.DB.SSLMode)
	assert.Equal(t, "5672", c.Rabbit.Port)
}

func TestConfigKeepsExplicitValues(t *testing.T) {
	c := (&Config{
		Server: ServerConfig{Host: "127.0.0.1", Port: "3000"},
		DB:     DBConfig{Port: "6432", MaxConns: 7, MaxIdle: 3, SSLMode: "require"},
	}).withDefaults()

	assert.Equal(t, "127.0.0.1:3000", c.Server.Addr())
	assert.Equal(t, "6432", c.DB.Port)
	assert.Equal(t, 7, c.DB.MaxConns)
	assert.Equal(t, "require", c.DB.SSLMode)
}

func TestRateLimitDefaults(t *testing.T) {
	// A disabled limiter still needs usable numbers, so turning it on is a
	// one word change rather than a tuning exercise.
	c := (&Config{}).withDefaults()

	assert.False(t, c.RateLimit.Enabled)
	assert.Equal(t, float64(1), c.RateLimit.RPS)
	assert.Equal(t, 5, c.RateLimit.Burst)
	assert.Equal(t, 10*time.Minute, c.RateLimit.IdleTTL)
}

func TestDBConfigDSN(t *testing.T) {
	dsn := DBConfig{
		Host: "db", Port: "5432", User: "app", Password: "secret", Name: "app", SSLMode: "require",
	}.DSN()

	assert.Equal(t, "host=db port=5432 user=app password=secret dbname=app sslmode=require", dsn)
}

func TestConfigAuthConfigMapping(t *testing.T) {
	c := Config{
		Auth: AuthConfig{
			AccessSecret:         "access",
			RefreshSecret:        "refresh",
			AccessTTL:            time.Minute,
			RefreshTTL:           time.Hour,
			RegistrationTokenTTL: time.Hour,
			Issuer:               "issuer",
			Audience:             "audience",
			BcryptCost:           4,
			ClockSkew:            time.Second,
		},
	}

	authCfg := c.AuthConfig()

	assert.Equal(t, "access", authCfg.AccessSecret)
	assert.Equal(t, "refresh", authCfg.RefreshSecret)
	assert.Equal(t, time.Minute, authCfg.AccessTTL)
	assert.Equal(t, time.Hour, authCfg.RefreshTTL)
	assert.Equal(t, time.Hour, authCfg.RegistrationTokenTTL)
	assert.Equal(t, "issuer", authCfg.Issuer)
	assert.Equal(t, "audience", authCfg.Audience)
	assert.Equal(t, 4, authCfg.BcryptCost)
	assert.Equal(t, time.Second, authCfg.ClockSkew)

	// What the yaml section produces has to be usable as is.
	require.NoError(t, authCfg.Validate())
}

// TestYAMLParsesDurations is what pins the yaml.v3 dependency: only v3 turns
// "15m" into a time.Duration, and yaml.v2 silently yields a zero duration,
// which would issue tokens that expire the moment they are minted.
func TestYAMLParsesDurations(t *testing.T) {
	raw := []byte(`
auth:
  access_ttl: 15m
  refresh_ttl: 168h
  clock_skew: 5s
rate_limit:
  idle_ttl: 10m
`)

	var c Config
	require.NoError(t, yaml.Unmarshal(raw, &c))

	assert.Equal(t, 15*time.Minute, c.Auth.AccessTTL)
	assert.Equal(t, 168*time.Hour, c.Auth.RefreshTTL)
	assert.Equal(t, 5*time.Second, c.Auth.ClockSkew)
	assert.Equal(t, 10*time.Minute, c.RateLimit.IdleTTL)
}

func TestYAMLParsesQueues(t *testing.T) {
	raw := []byte(`
rabbit:
  host: broker
  port: 5672
  user: u
  pwd: p
  queues:
    emails:
      durable: true
      exchange: email.exchange
      contentType: application/json
      prefetch: 5
`)

	var c Config
	require.NoError(t, yaml.Unmarshal(raw, &c))

	require.Contains(t, c.Rabbit.Queues, "emails")
	q := c.Rabbit.Queues["emails"]
	assert.True(t, q.Durable)
	assert.Equal(t, "email.exchange", q.Exchange)
	assert.Equal(t, "application/json", q.ContentType)
	assert.Equal(t, 5, q.Prefetch)
	assert.Equal(t, "amqp://u:p@broker:5672/", c.Rabbit.DSN())
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("ENV", "test")

	c, err := LoadConfig()

	require.NoError(t, err)
	require.NotNil(t, c)
	assert.NotEmpty(t, c.Server.Port)
	// The environment reaches the listener through the config, which is the
	// only thing deciding whether it serves TLS.
	assert.Equal(t, "test", c.Server.Env)
	assert.False(t, c.TLSEnabled())
	assert.NotEmpty(t, c.DB.Port)
	assert.NotEmpty(t, c.Rabbit.Port)

	// The shipped configuration has to yield a usable auth domain, since that is
	// what a fresh clone boots with.
	require.NoError(t, c.AuthConfig().Validate())
}

func TestTLSEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "local", env: "local", want: false},
		{name: "test", env: "test", want: false},
		{name: "LOCAL uppercase", env: "LOCAL", want: false},
		{name: "TEST uppercase", env: "TEST", want: false},
		{name: "padded", env: " test ", want: false},
		// Unset is the shipped default, and a hand-built Config skips
		// withDefaults, so it has to read the same way.
		{name: "unset", env: "", want: false},
		{name: "development", env: "development", want: true},
		{name: "staging", env: "staging", want: true},
		{name: "production", env: "production", want: true},
		{name: "prod", env: "prod", want: true},
		{name: "anything else", env: "qa", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{Server: ServerConfig{Env: tt.env}}
			assert.Equal(t, tt.want, c.TLSEnabled())
		})
	}
}

func TestTLSFiles(t *testing.T) {
	t.Run("disabled serves nothing", func(t *testing.T) {
		c := (&Config{Server: ServerConfig{Env: "local"}}).withDefaults()

		certFile, keyFile, err := c.TLSFiles()

		// The example config ships empty paths, so this is what every local run
		// and the whole test suite rely on.
		require.NoError(t, err)
		assert.Empty(t, certFile)
		assert.Empty(t, keyFile)
	})

	t.Run("an unset environment is not an error", func(t *testing.T) {
		c := &Config{}

		certFile, keyFile, err := c.TLSFiles()

		// Without withDefaults, as the server tests build it.
		require.NoError(t, err)
		assert.Empty(t, certFile)
		assert.Empty(t, keyFile)
	})

	t.Run("both paths are returned", func(t *testing.T) {
		c := (&Config{Server: ServerConfig{
			Env:      "production",
			CertFile: "/etc/tls/cert.pem",
			KeyFile:  "/etc/tls/key.pem",
		}}).withDefaults()

		certFile, keyFile, err := c.TLSFiles()

		require.NoError(t, err)
		assert.Equal(t, "/etc/tls/cert.pem", certFile)
		assert.Equal(t, "/etc/tls/key.pem", keyFile)
	})

	t.Run("a missing path is an error, not plaintext", func(t *testing.T) {
		tests := []struct {
			name     string
			certFile string
			keyFile  string
		}{
			{name: "neither"},
			{name: "cert only", certFile: "/etc/tls/cert.pem"},
			{name: "key only", keyFile: "/etc/tls/key.pem"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c := (&Config{Server: ServerConfig{
					Env:      "production",
					CertFile: tt.certFile,
					KeyFile:  tt.keyFile,
				}}).withDefaults()

				certFile, keyFile, err := c.TLSFiles()

				// Silently serving http where TLS was asked for is the one
				// outcome this must never produce.
				require.Error(t, err)
				assert.Contains(t, err.Error(), "production")
				assert.Empty(t, certFile)
				assert.Empty(t, keyFile)
			})
		}
	})
}

// compile time proof that the configuration carries the queue section the queue
// package expects, so the two cannot drift apart silently.
var _ = Config{Rabbit: queue.Config{}}
