package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTPAddr    string `yaml:"http_addr"`
	DatabaseURL string `yaml:"database_url"`

	TLSCertFile string `yaml:"tls_cert_file"`
	TLSKeyFile  string `yaml:"tls_key_file"`

	DefaultPoolSize          int    `yaml:"default_pool_size"`
	DefaultBaseImage         string `yaml:"default_base_image"`
	AdminToken               string `yaml:"admin_token"`
	EnrollmentToken          string `yaml:"enrollment_token"`
	WorkerPublicHost         string `yaml:"worker_public_hostname"`
	BrowserPublicHost        string `yaml:"browser_public_hostname"`
	AccessTeamDomain         string `yaml:"access_team_domain"`
	AccessAUD                string `yaml:"access_aud"`
	AccessIssuerURL          string `yaml:"access_issuer_url"`
	AccessJWKSURL            string `yaml:"access_jwks_url"`
	AccessAllowedEmailDomain string `yaml:"access_allowed_email_domain"`

	// Auto-update: require uploaded binaries to be signed with this identity
	RequiredSigningIdentity string `yaml:"required_signing_identity"`
	// Directory to store uploaded release binaries
	BinaryStoragePath string `yaml:"binary_storage_path"`
}

func DefaultConfig() *Config {
	return &Config{
		HTTPAddr:                 ":8080",
		DatabaseURL:              "postgres://localhost:5432/minicontrol?sslmode=disable",
		DefaultPoolSize:          2,
		DefaultBaseImage:         "minictl-tahoe-base",
		AdminToken:               "",
		EnrollmentToken:          "",
		WorkerPublicHost:         "",
		BrowserPublicHost:        "",
		AccessTeamDomain:         "",
		AccessAUD:                "",
		AccessIssuerURL:          "",
		AccessJWKSURL:            "",
		AccessAllowedEmailDomain: "",
		BinaryStoragePath:        "./releases",
	}
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
