// Package config loads server configuration from environment variables.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config is the fully resolved server configuration.
//
// Mail domains and SMTP parameters live in the database and are managed through
// /api/admin/settings, so they are intentionally absent here.
type Config struct {
	Addr         string
	DSN          string
	InboundToken string
	AdminToken   string

	// AttachmentDir stores MIME bodies and attachment payloads outside the
	// database. It can be a mount point (for example an rclone mount).
	AttachmentDir string

	// PublicBaseURL is the address clients reach the server on, for example
	// https://mail.example.com. Used to print absolute calendar links.
	PublicBaseURL string

	LogLevel string

	// AutoMigrate applies pending database migrations at startup. Turn it off
	// when "lifemail db migrate" runs as a separate deployment step.
	AutoMigrate bool

	// EASDumpDir keeps raw WBXML bodies for diagnostics when set.
	EASDumpDir string
}

// Load reads configuration from the environment.
func Load() Config {
	return Config{
		Addr:          env("LIFEMAIL_ADDR", ":8080"),
		DSN:           env("LIFEMAIL_DB_DSN", "lifemail.db"),
		InboundToken:  env("LIFEMAIL_INBOUND_TOKEN", ""),
		AdminToken:    env("LIFEMAIL_ADMIN_TOKEN", ""),
		AttachmentDir: env("LIFEMAIL_ATTACHMENT_DIR", "attachments"),
		PublicBaseURL: env("LIFEMAIL_PUBLIC_BASE_URL", ""),
		LogLevel:      env("LIFEMAIL_LOG_LEVEL", "info"),
		AutoMigrate:   envBool("LIFEMAIL_AUTO_MIGRATE", true),
		EASDumpDir:    env("LIFEMAIL_EAS_DUMP", ""),
	}
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n != 0
	}
	return def
}
