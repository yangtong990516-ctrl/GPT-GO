package util

import "os"

// EnvOrDefault returns the environment variable value or fallback when unset or
// empty. This is the unified env access helper; modules must not call os.Getenv
// directly for shared config.
func EnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
