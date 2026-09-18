// Package config loads and validates the YAML configuration (CONTRACT §3).
// The YAML file is the primary configuration carrier; environment variables may
// override specific fields but defaults live here.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config mirrors config.yaml.
type Config struct {
	Server  ServerConfig `yaml:"server"`
	MongoDB MongoConfig  `yaml:"mongodb"`
	App     AppConfig    `yaml:"app"`
	Log     LogConfig    `yaml:"log"`
}

// ServerConfig holds the HTTP listen address.
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// MongoConfig holds MongoDB connection settings.
type MongoConfig struct {
	URI       string `yaml:"uri"`
	Database  string `yaml:"database"`
	TimeoutMS int    `yaml:"timeout_ms"`
}

// AppConfig holds application metadata.
type AppConfig struct {
	Title   string `yaml:"title"`
	Version string `yaml:"version"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level string `yaml:"level"`
	JSON  bool   `yaml:"json"`
}

// Default returns the built-in default configuration, matching the values in
// config.yaml.
func Default() *Config {
	return &Config{
		Server: ServerConfig{Host: "127.0.0.1", Port: 8000},
		MongoDB: MongoConfig{
			URI:       "mongodb://127.0.0.1:27017",
			Database:  "autoregister",
			TimeoutMS: 10000,
		},
		App: AppConfig{Title: "AutoRegister Local Control Service", Version: "0.4.0"},
		Log: LogConfig{Level: "info", JSON: false},
	}
}

// Load reads and parses the YAML config at path. Unknown fields are rejected to
// keep the schema strict. If the file does not exist, Default() is returned.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate performs basic sanity checks on required fields.
func (c *Config) Validate() error {
	if c.Server.Host == "" {
		return fmt.Errorf("config: server.host must not be empty")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("config: server.port out of range")
	}
	if c.MongoDB.URI == "" {
		return fmt.Errorf("config: mongodb.uri must not be empty")
	}
	if c.MongoDB.Database == "" {
		return fmt.Errorf("config: mongodb.database must not be empty")
	}
	return nil
}
