// Package config contains the configuration structures for rest-server
package config

import (
	"fmt"
	"io/ioutil"
	"log"

	"github.com/PowerDNS/go-tlsconfig"
	"github.com/c2h5oh/datasize"
	"gopkg.in/yaml.v2"
)

// Config is the config root object
type Config struct {
	Path         string           `yaml:"path"`
	AppendOnly   bool             `yaml:"append_only"`
	PrivateRepos bool             `yaml:"private_repos"`
	Listen       string           `yaml:"listen"` // Address like ":8000"
	TLS          tlsconfig.Config `yaml:"tls"`
	AccessLog    string           `yaml:"access_log"`
	Debug        bool             `yaml:"debug"`
	Quota        Quota            `yaml:"quota"`
	Metrics      Metrics          `yaml:"metrics"`
	Auth         Auth             `yaml:"auth"`
	Users        map[string]User  `yaml:"users"`
}

// Quota configures disk usage quota enforcements
type Quota struct {
	Scope   string            `yaml:"scope,omitempty"`
	MaxSize datasize.ByteSize `yaml:"max_size"`
}

// Metrics configures Prometheus metrics
type Metrics struct {
	Enabled bool `yaml:"enabled"`
	NoAuth  bool `yaml:"no_auth"`
}

// Auth configures authentication
type Auth struct {
	Disabled     bool   `yaml:"disabled"`
	Backend      string `yaml:"backend,omitempty"`
	HTPasswdFile string `yaml:"htpasswd_file"`
}

// User configures user overrides
type User struct {
	AppendOnly   *bool `yaml:"append_only,omitempty"`
	PrivateRepos *bool `yaml:"private_repos,omitempty"`
}

// Check validates a Config instance
func (c Config) Check() error {
	return nil
}

// String returns the config as a YAML string
func (c Config) String() string {
	y, err := yaml.Marshal(c)
	if err != nil {
		log.Panicf("YAML marshal of config failed: %v", err) // Should never happen
	}
	return string(y)
}

// LoadYAML loads config from YAML. Any set value overwrites any existing value,
// but omitted keys are untouched.
func (c *Config) LoadYAML(yamlContents []byte) error {
	return yaml.UnmarshalStrict(yamlContents, c)
}

// LoadYAML loads config from a YAML file. Any set value overwrites any existing value,
// but omitted keys are untouched.
func (c *Config) LoadYAMLFile(fpath string) error {
	contents, err := ioutil.ReadFile(fpath)
	if err != nil {
		return fmt.Errorf("open yaml file: %w", err)
	}
	return c.LoadYAML(contents)
}

func mergeString(a, b string) string {
	if b != "" {
		return b
	}
	return a
}

// MergeFlags merges configuration set by commandline flags into the current Config
func (c *Config) MergeFlags(fc Config) {
	c.Debug = c.Debug || fc.Debug
	c.Listen = mergeString(c.Listen, fc.Listen)
	c.AccessLog = mergeString(c.AccessLog, fc.AccessLog)
	if fc.Quota.MaxSize > 0 {
		c.Quota.MaxSize = fc.Quota.MaxSize
	}
	c.Path = mergeString(c.Path, fc.Path)
	c.TLS.CertFile = mergeString(c.TLS.CertFile, fc.TLS.CertFile)
	c.TLS.KeyFile = mergeString(c.TLS.KeyFile, fc.TLS.KeyFile)
	c.Auth.Disabled = c.Auth.Disabled || fc.Auth.Disabled
	c.AppendOnly = c.AppendOnly || fc.AppendOnly
	c.PrivateRepos = c.PrivateRepos || fc.PrivateRepos
	c.Metrics.Enabled = c.Metrics.Enabled || fc.Metrics.Enabled
	c.Metrics.NoAuth = c.Metrics.NoAuth || fc.Metrics.NoAuth
}

// Default returns a Config with default settings
func Default() *Config {
	return &Config{
		Path:   "/tmp/restic",
		Listen: ":8000",
		Users:  make(map[string]User),
		Auth: Auth{
			Disabled:     false,
			Backend:      "htpasswd",
			HTPasswdFile: ".htpasswd",
		},
	}
}
