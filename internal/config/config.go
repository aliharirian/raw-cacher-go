package config

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	MinioEndpoint string `yaml:"minio_endpoint"`
	MinioAccess   string `yaml:"minio_access_key"`
	MinioSecret   string `yaml:"minio_secret_key"`
	MinioBucket   string `yaml:"minio_bucket"`

	TTLDefault int  `yaml:"ttl_default"`
	TTL404     int  `yaml:"ttl_404"`
	ServeIf    bool `yaml:"serve_if_present"`

	ListenAddr string `yaml:"listen_addr"`
}

func Load() (Config, error) {
	cfg := Config{
		TTLDefault:  3600,
		TTL404:      60,
		ServeIf:     false,
		ListenAddr:  ":8080",
		MinioBucket: "proxy-cache",
	}
	path := os.Getenv("RAW_CACHER_CONFIG")
	if path == "" {
		path = "config.yaml"
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(b, &cfg)
	}
	if v := os.Getenv("MINIO_ENDPOINT"); v != "" {
		cfg.MinioEndpoint = v
	}
	if v := os.Getenv("MINIO_ACCESS_KEY"); v != "" {
		cfg.MinioAccess = v
	}
	if v := os.Getenv("MINIO_SECRET_KEY"); v != "" {
		cfg.MinioSecret = v
	}
	if v := os.Getenv("MINIO_BUCKET"); v != "" {
		cfg.MinioBucket = v
	}
	if v := os.Getenv("TTL_DEFAULT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.TTLDefault = n
		}
	}
	if v := os.Getenv("TTL_404"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.TTL404 = n
		}
	}
	if v := os.Getenv("SERVE_IF_PRESENT"); v != "" {
		cfg.ServeIf = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if cfg.MinioEndpoint == "" || cfg.MinioAccess == "" || cfg.MinioSecret == "" || cfg.MinioBucket == "" {
		return cfg, errors.New("minio config incomplete (endpoint/access/secret/bucket)")
	}
	return cfg, nil
}
