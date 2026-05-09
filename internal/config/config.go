package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
)

type Service struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Config struct {
	ListenAddr      string
	ProxyTarget     string
	OutboundTimeout time.Duration
	ShutdownTimeout time.Duration
	HealthInterval  time.Duration
	HealthTimeout   time.Duration
	HealthRetries   int
	HealthServices  []Service
	RateLimitRPS    int
	RateLimitBurst  int
	CBFailThreshold int
	CBCooldown      time.Duration
	LogLevel        zapcore.Level
	OTLPEndpoint    string
	TraceSampleRate float64
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:      getEnv("SIDECAR_LISTEN_ADDR", ":8080"),
		ProxyTarget:     getEnv("SIDECAR_PROXY_TARGET", "http://127.0.0.1:8081"),
		OutboundTimeout: getDuration("SIDECAR_OUTBOUND_TIMEOUT", 2*time.Second),
		ShutdownTimeout: getDuration("SIDECAR_SHUTDOWN_TIMEOUT", 10*time.Second),
		HealthInterval:  getDuration("SIDECAR_HEALTH_INTERVAL", 5*time.Second),
		HealthTimeout:   getDuration("SIDECAR_HEALTH_TIMEOUT", 1200*time.Millisecond),
		HealthRetries:   getInt("SIDECAR_HEALTH_RETRIES", 1),
		RateLimitRPS:    getInt("SIDECAR_RATE_LIMIT_RPS", 250),
		RateLimitBurst:  getInt("SIDECAR_RATE_LIMIT_BURST", 500),
		CBFailThreshold: getInt("SIDECAR_CB_FAIL_THRESHOLD", 3),
		CBCooldown:      getDuration("SIDECAR_CB_COOLDOWN", 15*time.Second),
		LogLevel:        getLogLevel("SIDECAR_LOG_LEVEL", "info"),
		OTLPEndpoint:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		TraceSampleRate: getFloat("SIDECAR_TRACE_SAMPLE_RATE", 0.1),
	}

	services, err := loadServices()
	if err != nil {
		return Config{}, err
	}
	cfg.HealthServices = services
	if len(cfg.HealthServices) == 0 {
		return Config{}, errors.New("no health services configured; set SIDECAR_HEALTH_SERVICES or SIDECAR_HEALTH_CONFIG_PATH")
	}
	return cfg, nil
}

func loadServices() ([]Service, error) {
	if raw := strings.TrimSpace(os.Getenv("SIDECAR_HEALTH_SERVICES")); raw != "" {
		var out []Service
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, fmt.Errorf("parse SIDECAR_HEALTH_SERVICES: %w", err)
		}
		return out, nil
	}

	if path := strings.TrimSpace(os.Getenv("SIDECAR_HEALTH_CONFIG_PATH")); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read SIDECAR_HEALTH_CONFIG_PATH: %w", err)
		}
		var out []Service
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("parse health config file: %w", err)
		}
		return out, nil
	}
	return nil, nil
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil {
			return val
		}
	}
	return fallback
}

func getFloat(key string, fallback float64) float64 {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if val, err := strconv.ParseFloat(raw, 64); err == nil {
			return val
		}
	}
	return fallback
}

func getLogLevel(key, fallback string) zapcore.Level {
	level := zapcore.InfoLevel
	val := getEnv(key, fallback)
	if err := level.UnmarshalText([]byte(val)); err == nil {
		return level
	}
	return zapcore.InfoLevel
}
