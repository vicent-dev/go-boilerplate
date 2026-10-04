package app

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"go-boilerplate/pkg/auth"
	"go-boilerplate/pkg/log"
	"go-boilerplate/pkg/queue"
	"go-boilerplate/static"
)

// ServerConfig is the http listener configuration.
type ServerConfig struct {
	Host string `yaml:"host"`
	Port string `yaml:"port"`
}

// Addr is the listen address of the http server.
func (c ServerConfig) Addr() string {
	return c.Host + ":" + c.Port
}

// RateLimitConfig throttles the auth endpoints per client address.
//
// It is per IP rather than global on purpose: a single bucket for every caller
// is not a limit, it is an outage waiting for a second concurrent user.
type RateLimitConfig struct {
	Enabled bool `yaml:"enabled"`
	// RPS is the sustained rate per client address. Values below 1 are
	// meaningful: 0.05 is one request every twenty seconds.
	RPS float64 `yaml:"rps"`
	// Burst is how many requests a client may send at once before it settles
	// into the sustained rate.
	Burst int `yaml:"burst"`
	// IdleTTL is how long an address with no requests keeps its bucket before
	// it is swept. It bounds the memory a flood of addresses can pin.
	IdleTTL time.Duration `yaml:"idle_ttl"`
}

func (c RateLimitConfig) withDefaults() RateLimitConfig {
	if c.RPS <= 0 {
		c.RPS = 1
	}
	if c.Burst <= 0 {
		c.Burst = 5
	}
	if c.IdleTTL <= 0 {
		c.IdleTTL = 10 * time.Minute
	}
	return c
}

// DBConfig is the relational store configuration.
type DBConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"`
	MaxConns int    `yaml:"max_conns"`
	MaxIdle  int    `yaml:"max_idle"`
}

// DSN renders the postgres connection string.
func (c DBConfig) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host,
		c.Port,
		c.User,
		c.Password,
		c.Name,
		c.SSLMode,
	)
}

// AuthConfig is the yaml shape of the auth domain configuration. It is mapped
// to auth.Config, which is what the domain actually consumes: defaults for the
// domain live there, not here.
type AuthConfig struct {
	AccessSecret         string        `yaml:"access_secret"`
	RefreshSecret        string        `yaml:"refresh_secret"`
	AccessTTL            time.Duration `yaml:"access_ttl"`
	RefreshTTL           time.Duration `yaml:"refresh_ttl"`
	RegistrationTokenTTL time.Duration `yaml:"registration_token_ttl"`
	Issuer               string        `yaml:"issuer"`
	Audience             string        `yaml:"audience"`
	BcryptCost           int           `yaml:"bcrypt_cost"`
	ClockSkew            time.Duration `yaml:"clock_skew"`
}

// Config is the whole application configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	DB        DBConfig        `yaml:"db"`
	Auth      AuthConfig      `yaml:"auth"`
	Rabbit    queue.Config    `yaml:"rabbit"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

// AuthConfig maps the yaml section to the auth domain configuration.
func (c Config) AuthConfig() auth.Config {
	return auth.Config{
		AccessSecret:         c.Auth.AccessSecret,
		RefreshSecret:        c.Auth.RefreshSecret,
		AccessTTL:            c.Auth.AccessTTL,
		RefreshTTL:           c.Auth.RefreshTTL,
		RegistrationTokenTTL: c.Auth.RegistrationTokenTTL,
		Issuer:               c.Auth.Issuer,
		Audience:             c.Auth.Audience,
		BcryptCost:           c.Auth.BcryptCost,
		ClockSkew:            c.Auth.ClockSkew,
	}
}

func (c Config) withDefaults() Config {
	if c.Server.Port == "" {
		c.Server.Port = "8080"
	}
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.DB.MaxConns == 0 {
		c.DB.MaxConns = 100
	}
	if c.DB.MaxIdle == 0 {
		c.DB.MaxIdle = 10
	}
	if c.DB.SSLMode == "" {
		c.DB.SSLMode = "disable"
	}
	if c.DB.Port == "" {
		c.DB.Port = "5432"
	}
	if c.Rabbit.Port == "" {
		c.Rabbit.Port = "5672"
	}
	c.RateLimit = c.RateLimit.withDefaults()
	return c
}

// LoadConfig reads the embedded configuration file, expanding environment
// variables and applying defaults. It is exported so that every command shares
// exactly the same configuration as the server.
func LoadConfig() (*Config, error) {
	// A missing .env is not an error: the configuration may come entirely from
	// the embedded file and the process environment.
	if err := godotenv.Load(); err != nil {
		log.LogWarn(context.Background(), "no .env file loaded: "+err.Error())
	}

	expanded := expandEnv(string(static.GetConfigFile()))

	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	c = c.withDefaults()
	return &c, nil
}

// envRef matches ${VAR} and ${VAR:-default}. Anything else, like a nested
// expansion, is left untouched so a typo shows up in the loaded configuration
// instead of silently becoming empty.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-(.*?))?\}`)

// expandEnv resolves environment variables in the configuration, falling back to
// the default written in the file when the variable is unset or empty.
//
// os.ExpandEnv cannot be used for this: it reads ${VAR:-default} as a variable
// named "VAR:-default", which always resolves to an empty string.
func expandEnv(in string) string {
	return envRef.ReplaceAllStringFunc(in, func(ref string) string {
		match := envRef.FindStringSubmatch(ref)
		if value := os.Getenv(match[1]); value != "" {
			return value
		}
		return match[2]
	})
}
