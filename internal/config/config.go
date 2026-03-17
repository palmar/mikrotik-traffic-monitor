package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	Devices        []DeviceConfig `yaml:"devices"`
	PollInterval   Duration       `yaml:"poll_interval"`
	RingBufferSize int            `yaml:"ring_buffer_size"`
	ListenAddr     string         `yaml:"listen_addr"`
	Report         ReportConfig   `yaml:"report"`
}

// ReportConfig holds weekly email report settings.
type ReportConfig struct {
	ResendAPIKey string `yaml:"resend_api_key"`
	FromAddr     string `yaml:"from_addr"`
	Timezone     string `yaml:"report_timezone"`
	DayOfWeek    int    `yaml:"report_day_of_week"` // 0=Sunday, 6=Saturday
	Hour         int    `yaml:"report_hour"`        // 0-23
}

// DeviceConfig holds connection settings for a single Mikrotik device.
type DeviceConfig struct {
	Name        string `yaml:"name"`
	Host        string `yaml:"host"`
	Port        uint16 `yaml:"port"`
	SNMPVersion string `yaml:"snmp_version"` // "v2c" or "v3" (default: "v3")
	Community   string `yaml:"community"`    // SNMPv2c community string
	Username    string `yaml:"username"`     // SNMPv3 username
	AuthPass     string `yaml:"auth_pass"`     // SNMPv3 auth passphrase
	PrivPass     string `yaml:"priv_pass"`     // SNMPv3 privacy passphrase
	AuthProtocol string `yaml:"auth_protocol"` // SNMPv3 auth protocol: sha1, sha256 (default: sha1)
	PrivProtocol string `yaml:"priv_protocol"` // SNMPv3 privacy protocol: aes, des (default: aes)
	OwnerEmail   string `yaml:"owner_email"`   // email address for weekly reports
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
		PollInterval:   Duration(5 * time.Second),
		RingBufferSize: 240,
		ListenAddr:     ":8080",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if len(cfg.Devices) == 0 {
		return nil, fmt.Errorf("at least one device must be configured")
	}

	names := make(map[string]bool)
	for i, dev := range cfg.Devices {
		if dev.Host == "" {
			return nil, fmt.Errorf("devices[%d].host is required", i)
		}
		if dev.Name == "" {
			cfg.Devices[i].Name = dev.Host
		}
		if dev.Port == 0 {
			cfg.Devices[i].Port = 161
		}
		// Default SNMP version to v3
		switch dev.SNMPVersion {
		case "":
			cfg.Devices[i].SNMPVersion = "v3"
			dev.SNMPVersion = "v3"
		case "v2c", "v3":
			// valid
		default:
			return nil, fmt.Errorf("devices[%d].snmp_version must be \"v2c\" or \"v3\"", i)
		}
		// Validate version-specific fields
		if dev.SNMPVersion == "v2c" {
			if dev.Community == "" {
				return nil, fmt.Errorf("devices[%d].community is required for SNMPv2c", i)
			}
		} else {
			if dev.Username == "" {
				return nil, fmt.Errorf("devices[%d].username is required for SNMPv3", i)
			}
			switch dev.AuthProtocol {
			case "":
				cfg.Devices[i].AuthProtocol = "sha1"
			case "sha1", "sha256":
				// valid
			default:
				return nil, fmt.Errorf("devices[%d].auth_protocol must be \"sha1\" or \"sha256\"", i)
			}
			switch dev.PrivProtocol {
			case "":
				cfg.Devices[i].PrivProtocol = "aes"
			case "aes", "des":
				// valid
			default:
				return nil, fmt.Errorf("devices[%d].priv_protocol must be \"aes\" or \"des\"", i)
			}
		}
		name := cfg.Devices[i].Name
		if names[name] {
			return nil, fmt.Errorf("duplicate device name %q", name)
		}
		names[name] = true
	}

	if cfg.RingBufferSize < 1 {
		return nil, fmt.Errorf("ring_buffer_size must be >= 1")
	}

	// Validate report config if partially set
	r := cfg.Report
	if r.ResendAPIKey != "" || r.FromAddr != "" {
		if r.ResendAPIKey == "" {
			return nil, fmt.Errorf("report.resend_api_key is required when report is configured")
		}
		if r.FromAddr == "" {
			return nil, fmt.Errorf("report.from_addr is required when report is configured")
		}
		if r.Timezone == "" {
			cfg.Report.Timezone = "UTC"
		}
		if _, err := time.LoadLocation(cfg.Report.Timezone); err != nil {
			return nil, fmt.Errorf("report.report_timezone %q: %w", cfg.Report.Timezone, err)
		}
		if r.DayOfWeek < 0 || r.DayOfWeek > 6 {
			return nil, fmt.Errorf("report.report_day_of_week must be 0-6")
		}
		if r.Hour < 0 || r.Hour > 23 {
			return nil, fmt.Errorf("report.report_hour must be 0-23")
		}
	}

	return cfg, nil
}
