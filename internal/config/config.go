package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	defaultRunAddress = "localhost:8080"
)

type Config struct {
	RunAddress           string
	DatabaseURI          string
	AccrualSystemAddress string
	AuthSecret           string
	CookieSecure         bool
}

func Parse(args []string) (Config, error) {
	cfg := Config{
		RunAddress: defaultRunAddress,
	}

	dotEnv, err := godotenv.Read()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read .env: %w", err)
	}
	applyDotEnv(&cfg.RunAddress, "RUN_ADDRESS", dotEnv)
	applyDotEnv(&cfg.DatabaseURI, "DATABASE_URI", dotEnv)
	applyDotEnv(&cfg.AccrualSystemAddress, "ACCRUAL_SYSTEM_ADDRESS", dotEnv)
	applyDotEnv(&cfg.AuthSecret, "AUTH_SECRET", dotEnv)

	applyEnv(&cfg.RunAddress, "RUN_ADDRESS")
	applyEnv(&cfg.DatabaseURI, "DATABASE_URI")
	applyEnv(&cfg.AccrualSystemAddress, "ACCRUAL_SYSTEM_ADDRESS")
	applyEnv(&cfg.AuthSecret, "AUTH_SECRET")

	cookieSecure := "false" // Local development and course autotests use HTTP.
	applyDotEnv(&cookieSecure, "COOKIE_SECURE", dotEnv)
	applyEnv(&cookieSecure, "COOKIE_SECURE")
	cfg.CookieSecure, err = strconv.ParseBool(strings.TrimSpace(cookieSecure))
	if err != nil {
		return Config{}, fmt.Errorf("parse COOKIE_SECURE: %w", err)
	}

	flags := flag.NewFlagSet("gophermart", flag.ContinueOnError)
	flags.BoolVar(&cfg.CookieSecure, "cookie-secure", cfg.CookieSecure, "send session cookie only over HTTPS")
	flags.StringVar(&cfg.RunAddress, "a", cfg.RunAddress, "server address")
	flags.StringVar(&cfg.DatabaseURI, "d", cfg.DatabaseURI, "PostgreSQL connection URI")
	flags.StringVar(&cfg.AccrualSystemAddress, "r", cfg.AccrualSystemAddress, "accrual system address")

	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	cfg.RunAddress = strings.TrimSpace(cfg.RunAddress)
	cfg.DatabaseURI = strings.TrimSpace(cfg.DatabaseURI)
	cfg.AccrualSystemAddress = strings.TrimRight(strings.TrimSpace(cfg.AccrualSystemAddress), "/")
	cfg.AuthSecret = strings.TrimSpace(cfg.AuthSecret)

	return cfg, nil
}

func (c Config) Validate() error {
	var validationErrors []error

	if c.RunAddress == "" {
		validationErrors = append(validationErrors, errors.New("run address is required"))
	} else if _, _, err := net.SplitHostPort(c.RunAddress); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("invalid run address: %w", err))
	}
	if c.DatabaseURI == "" {
		validationErrors = append(validationErrors, errors.New("database URI is required"))
	}
	if c.AccrualSystemAddress == "" {
		validationErrors = append(validationErrors, errors.New("accrual system address is required"))
	} else if parsedURL, err := url.ParseRequestURI(c.AccrualSystemAddress); err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		validationErrors = append(validationErrors, errors.New("invalid accrual system address"))
	}
	if c.AuthSecret == "" {
		validationErrors = append(validationErrors, errors.New("authentication secret is required"))
	}

	return errors.Join(validationErrors...)
}

func applyEnv(target *string, key string) {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		*target = value
	}
}

func applyDotEnv(target *string, key string, values map[string]string) {
	if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
		*target = value
	}
}
