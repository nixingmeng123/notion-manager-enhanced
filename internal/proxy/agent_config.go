package proxy

import (
	"fmt"
	"sync"
	"time"
)

type AgentV3Config struct {
	Enabled           bool `yaml:"enabled" json:"enabled"`
	TimeoutSeconds    int  `yaml:"timeout_seconds" json:"timeout_seconds"`
	PollIntervalMS    int  `yaml:"poll_interval_ms" json:"poll_interval_ms"`
	ModelCacheSeconds int  `yaml:"model_cache_seconds" json:"model_cache_seconds"`
}

type enhancedConfig struct {
	AgentV3Config
	DefaultProtocol string
}

var enhancedConfigMu sync.RWMutex

func enhancedConfigSnapshot() enhancedConfig {
	enhancedConfigMu.RLock()
	defer enhancedConfigMu.RUnlock()
	cfg := enhancedConfig{AgentV3Config: AgentV3Config{TimeoutSeconds: 300, PollIntervalMS: 3000, ModelCacheSeconds: 300}, DefaultProtocol: "workflow"}
	if AppConfig != nil {
		cfg.Enabled = AppConfig.AgentV3.Enabled
		if AppConfig.AgentV3.TimeoutSeconds > 0 {
			cfg.TimeoutSeconds = AppConfig.AgentV3.TimeoutSeconds
		}
		if AppConfig.AgentV3.PollIntervalMS > 0 {
			cfg.PollIntervalMS = AppConfig.AgentV3.PollIntervalMS
		}
		if AppConfig.AgentV3.ModelCacheSeconds > 0 {
			cfg.ModelCacheSeconds = AppConfig.AgentV3.ModelCacheSeconds
		}
		if AppConfig.Proxy.DefaultProtocol != "" {
			cfg.DefaultProtocol = AppConfig.Proxy.DefaultProtocol
		}
	}
	return cfg
}

func validateEnhancedConfig(cfg enhancedConfig) error {
	if cfg.DefaultProtocol != "workflow" && cfg.DefaultProtocol != "agent" {
		return fmt.Errorf("default_protocol must be workflow or agent")
	}
	if cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > 1800 {
		return fmt.Errorf("agent_timeout_seconds must be between 1 and 1800")
	}
	if cfg.PollIntervalMS < 100 || cfg.PollIntervalMS > 30000 {
		return fmt.Errorf("agent_poll_interval_ms must be between 100 and 30000")
	}
	if cfg.ModelCacheSeconds < 1 || cfg.ModelCacheSeconds > 86400 {
		return fmt.Errorf("agent model cache must be between 1 and 86400 seconds")
	}
	if cfg.DefaultProtocol == "agent" && !cfg.Enabled {
		return fmt.Errorf("enable Agent V3 before setting it as the default protocol")
	}
	return nil
}

func agentTimeout() time.Duration {
	return time.Duration(enhancedConfigSnapshot().TimeoutSeconds) * time.Second
}
