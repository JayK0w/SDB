// Package config loads and validates the SDB runtime configuration.
//
// Configuration is environment-first (12-factor): every value is set
// through an SDB_* variable, which suits a hardened container deployment.
// Secrets additionally support the *_FILE convention so they can be
// mounted as Docker secrets instead of appearing in the environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DataDir     string
	Server      Server
	Database    Database
	Docker      Docker
	Auth        Auth
	Maintenance Maintenance
	Log         Log
}

type Server struct {
	Host string
	Port int
}

func (s Server) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// IsLoopback reports whether the listen address is loopback-only. Binding
// wider than loopback is allowed (needed inside a container) but the
// caller should warn, because Docker port publishing bypasses UFW/iptables.
func (s Server) IsLoopback() bool {
	if s.Host == "localhost" {
		return true
	}
	ip := net.ParseIP(s.Host)
	return ip != nil && ip.IsLoopback()
}

type Database struct {
	Path string
}

type Docker struct {
	// Host is the daemon endpoint; empty means the SDK default (local
	// unix socket / npipe).
	Host        string
	TLSVerify   bool
	TLSCACert   string
	TLSCert     string
	TLSKey      string
	WorkerImage string
	// StopTimeout is how long a container gets to stop gracefully before
	// the daemon kills it (cold backups, hooks that stop services).
	StopTimeout time.Duration
}

type Auth struct {
	// JWTSecret signs API access tokens.
	JWTSecret string
	TokenTTL  time.Duration
	// MasterKey encrypts storage credentials and Restic passwords at rest
	// (AES-256-GCM). Losing it makes stored storage configs unreadable.
	MasterKey string
	// AdminUsername/AdminPassword seed the first admin account when the
	// user table is empty. Without a password, a random one is generated
	// and printed once in the logs.
	AdminUsername string
	AdminPassword string
}

type Maintenance struct {
	// CheckInterval is the pause between scheduled repository integrity
	// checks (restic check); 0 disables them.
	CheckInterval time.Duration
}

type Log struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

func Load() (*Config, error) {
	dataDir := getenv("SDB_DATA_DIR", "data")

	port, err := getenvInt("SDB_LISTEN_PORT", 8080)
	if err != nil {
		return nil, err
	}
	stopTimeout, err := getenvDuration("SDB_CONTAINER_STOP_TIMEOUT", 30*time.Second)
	if err != nil {
		return nil, err
	}
	tokenTTL, err := getenvDuration("SDB_TOKEN_TTL", time.Hour)
	if err != nil {
		return nil, err
	}
	tlsVerify, err := getenvBool("SDB_DOCKER_TLS_VERIFY", false)
	if err != nil {
		return nil, err
	}
	masterKey, err := getSecret("SDB_MASTER_KEY")
	if err != nil {
		return nil, err
	}
	jwtSecret, err := getSecret("SDB_JWT_SECRET")
	if err != nil {
		return nil, err
	}
	adminPassword, err := getSecret("SDB_ADMIN_PASSWORD")
	if err != nil {
		return nil, err
	}
	checkInterval, err := getenvDuration("SDB_CHECK_INTERVAL", 168*time.Hour)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		DataDir: dataDir,
		Server: Server{
			Host: getenv("SDB_LISTEN_HOST", "127.0.0.1"),
			Port: port,
		},
		Database: Database{
			Path: getenv("SDB_DB_PATH", filepath.Join(dataDir, "sdb.db")),
		},
		Docker: Docker{
			Host:        os.Getenv("SDB_DOCKER_HOST"),
			TLSVerify:   tlsVerify,
			TLSCACert:   os.Getenv("SDB_DOCKER_TLS_CA"),
			TLSCert:     os.Getenv("SDB_DOCKER_TLS_CERT"),
			TLSKey:      os.Getenv("SDB_DOCKER_TLS_KEY"),
			WorkerImage: getenv("SDB_WORKER_IMAGE", "restic/restic:0.18.0"),
			StopTimeout: stopTimeout,
		},
		Auth: Auth{
			JWTSecret:     jwtSecret,
			TokenTTL:      tokenTTL,
			MasterKey:     masterKey,
			AdminUsername: getenv("SDB_ADMIN_USERNAME", "admin"),
			AdminPassword: adminPassword,
		},
		Maintenance: Maintenance{
			CheckInterval: checkInterval,
		},
		Log: Log{
			Level:  getenv("SDB_LOG_LEVEL", "info"),
			Format: getenv("SDB_LOG_FORMAT", "json"),
		},
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("SDB_LISTEN_PORT: %d is not a valid port", c.Server.Port))
	}
	if len(c.Auth.MasterKey) < 32 {
		errs = append(errs, errors.New("SDB_MASTER_KEY (or SDB_MASTER_KEY_FILE) must be set to at least 32 characters — generate one with `openssl rand -hex 32`"))
	}
	if len(c.Auth.JWTSecret) < 32 {
		errs = append(errs, errors.New("SDB_JWT_SECRET (or SDB_JWT_SECRET_FILE) must be set to at least 32 characters — generate one with `openssl rand -hex 32`"))
	}
	if c.Auth.TokenTTL <= 0 {
		errs = append(errs, errors.New("SDB_TOKEN_TTL must be a positive duration"))
	}
	if c.Auth.AdminPassword != "" && len(c.Auth.AdminPassword) < 12 {
		errs = append(errs, errors.New("SDB_ADMIN_PASSWORD must be at least 12 characters (or left empty to generate one)"))
	}
	if c.Maintenance.CheckInterval < 0 {
		errs = append(errs, errors.New("SDB_CHECK_INTERVAL must be zero (disabled) or a positive duration"))
	}
	// Plaintext TCP to the Docker daemon is root-equivalent remote access:
	// refuse it outright instead of warning.
	if strings.HasPrefix(c.Docker.Host, "tcp://") {
		if !c.Docker.TLSVerify || c.Docker.TLSCACert == "" || c.Docker.TLSCert == "" || c.Docker.TLSKey == "" {
			errs = append(errs, errors.New("SDB_DOCKER_HOST uses tcp:// — unencrypted TCP is forbidden; set SDB_DOCKER_TLS_VERIFY=true and provide SDB_DOCKER_TLS_CA, SDB_DOCKER_TLS_CERT and SDB_DOCKER_TLS_KEY (mTLS)"))
		}
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("SDB_LOG_LEVEL: unknown level %q", c.Log.Level))
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("SDB_LOG_FORMAT: unknown format %q", c.Log.Format))
	}

	return errors.Join(errs...)
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: expected an integer, got %q", key, v)
	}
	return n, nil
}

func getenvBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: expected a boolean, got %q", key, v)
	}
	return b, nil
}

func getenvDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: expected a duration such as 30s or 1h, got %q", key, v)
	}
	return d, nil
}

// getSecret resolves KEY_FILE first (Docker secrets), then KEY.
func getSecret(key string) (string, error) {
	if path := os.Getenv(key + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", key, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return os.Getenv(key), nil
}
