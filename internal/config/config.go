package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	Router         RouterConfig `yaml:"router"`
	Interfaces     []string     `yaml:"interfaces"`
	PollInterval   Duration     `yaml:"poll_interval"`
	RingBufferSize int          `yaml:"ring_buffer_size"`
	ListenAddr     string       `yaml:"listen_addr"`
}

// RouterConfig holds SNMP connection settings for the Mikrotik router.
type RouterConfig struct {
	Host     string `yaml:"host"`
	Port     uint16 `yaml:"port"`
	Username string `yaml:"username"`
	AuthPass string `yaml:"auth_pass"`
	PrivPass string `yaml:"priv_pass"`
}

// Duration wraps time.Duration for YAML unmarshaling (e.g. "5s", "10s").
type Duration time.Duration

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// Load reads and parses a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &Config{
		Router:         RouterConfig{Port: 161},
		PollInterval:   Duration(5 * time.Second),
		RingBufferSize: 240,
		ListenAddr:     ":8080",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Router.Host == "" {
		return nil, fmt.Errorf("router.host is required")
	}
	if cfg.Router.Username == "" {
		return nil, fmt.Errorf("router.username is required")
	}
	if len(cfg.Interfaces) == 0 {
		return nil, fmt.Errorf("at least one interface must be configured")
	}
	if cfg.RingBufferSize < 1 {
		return nil, fmt.Errorf("ring_buffer_size must be >= 1")
	}

	return cfg, nil
}
