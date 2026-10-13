package dashboard

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fadhlanhawali/grepalert/internal/rules"
)

// Config holds the dashboard's own settings. Agents and groups are managed in
// the UI and saved to DataFile.
type Config struct {
	Listen  string `yaml:"listen"`
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
	Auth    Auth   `yaml:"auth"`
	// DataFile stores agents, groups and group rules (default dashboard-data.json).
	DataFile string `yaml:"data_file"`
	// ReconcileInterval is how often group members are checked and re-pushed
	// the group's rules if they drifted or came back online (default 30s).
	ReconcileInterval rules.Duration `yaml:"reconcile_interval"`
	// EnrollmentToken is an optional fixed token agents can enroll with, for
	// automated setups (Ansible, Terraform, images). Tokens can also be
	// created and revoked in the UI.
	EnrollmentToken string `yaml:"enrollment_token"`
}

// Auth enables HTTP basic auth on the dashboard when a password is set.
type Auth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	raw = []byte(os.Expand(string(raw), os.Getenv))
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.setDefaults()
	return c, nil
}

func (c *Config) setDefaults() {
	if c.Listen == "" {
		c.Listen = ":9400"
	}
	if c.DataFile == "" {
		c.DataFile = "dashboard-data.json"
	}
	if c.ReconcileInterval <= 0 {
		c.ReconcileInterval = rules.Duration(30 * time.Second)
	}
}
