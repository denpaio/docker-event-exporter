// Package config loads and validates runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultCooldown  = 30 * time.Second
	defaultStopGrace = 30 * time.Second
)

// Config holds the validated runtime configuration.
type Config struct {
	SlackToken   string
	SlackChannel string
	NodeName     string
	Cooldown     time.Duration
	// StopGrace is how long after a stop signal a container's death is still
	// treated as an intentional stop (Ctrl-C, docker stop / compose down) rather
	// than a crash worth notifying about.
	StopGrace time.Duration
	// IgnoreExitCodes is the set of container "die" exit codes that must NOT
	// trigger a notification. Exit code "0" is always present.
	IgnoreExitCodes map[string]struct{}
}

// Load reads configuration from environment variables, applying defaults and
// failing fast when required values are missing or malformed.
func Load() (*Config, error) {
	cfg := &Config{
		SlackToken:      os.Getenv("SLACK_BOT_TOKEN"),
		SlackChannel:    os.Getenv("SLACK_CHANNEL"),
		NodeName:        os.Getenv("NODE_NAME"),
		Cooldown:        defaultCooldown,
		StopGrace:       defaultStopGrace,
		IgnoreExitCodes: parseIgnoreExitCodes(os.Getenv("IGNORE_EXIT_CODES")),
	}

	var missing []string
	if cfg.SlackToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.SlackChannel == "" {
		missing = append(missing, "SLACK_CHANNEL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if cfg.NodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("resolve hostname: %w", err)
		}
		cfg.NodeName = hostname
	}

	if v := os.Getenv("EVENT_COOLDOWN"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid EVENT_COOLDOWN %q: %w", v, err)
		}
		cfg.Cooldown = d
	}

	if v := os.Getenv("STOP_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid STOP_GRACE %q: %w", v, err)
		}
		cfg.StopGrace = d
	}

	return cfg, nil
}

// parseIgnoreExitCodes builds the ignore set from a comma-separated list. Exit
// code "0" (a clean exit) is always ignored, so a normal container stop never
// produces a notification regardless of the configured value.
func parseIgnoreExitCodes(raw string) map[string]struct{} {
	codes := map[string]struct{}{"0": {}}
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			codes[part] = struct{}{}
		}
	}
	return codes
}
