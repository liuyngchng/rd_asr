package config

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type SysConfig struct {
	Name          string `yaml:"name"`
	Auth          bool   `yaml:"auth"`
	CfgToken      string `yaml:"cfg_tkn"`
	AllowedOrigin string `yaml:"allowed_origin"`
	CypherKey     string `yaml:"cypher_key"`
	// FileRetentionDays 磁盘文件保留天数，超过后自动删除（0 表示用默认值 30）
	FileRetentionDays int `yaml:"file_retention_days"`
}

type ApiConfig struct {
	AuthAPI      string `yaml:"auth_api"`
	AsrHTTPAPI   string `yaml:"asr_http_api_uri"`
	AsrWSAPI     string `yaml:"asr_ws_api_uri"`
	AsrAPIKey    string `yaml:"asr_api_key"`
	AsrModelName string `yaml:"asr_model_name"`
}

type FunasrConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	HTTPAPIURI string `yaml:"http_api_uri"`
	WSAPIURI   string `yaml:"ws_api_uri"`
}

type Config struct {
	Sys    SysConfig    `yaml:"sys"`
	Api    ApiConfig    `yaml:"api"`
	Funasr FunasrConfig `yaml:"funasr"`
}

var (
	global  *Config
	once    sync.Once
	loadErr error
)

func Load() (*Config, error) {
	once.Do(func() {
		data, err := os.ReadFile("cfg.yml")
		if err != nil {
			loadErr = fmt.Errorf("read cfg.yml: %w", err)
			return
		}
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			loadErr = fmt.Errorf("parse cfg.yml: %w", err)
			return
		}
		if cfg.Sys.CypherKey == "" {
			loadErr = fmt.Errorf("sys.cypher_key is required in cfg.yml")
			return
		}
		if cfg.Sys.FileRetentionDays == 0 {
			cfg.Sys.FileRetentionDays = 30
		}
		global = &cfg
	})
	if loadErr != nil {
		return nil, loadErr
	}
	return global, nil
}