package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv("RUN_ADDRESS", "127.0.0.1:9000")
	t.Setenv("DATABASE_URI", "postgres://environment")
	t.Setenv("ACCRUAL_SYSTEM_ADDRESS", "http://accrual.local/")
	t.Setenv("AUTH_SECRET", "test-secret")

	cfg, err := Parse([]string{
		"-a", "localhost:8000",
		"-d", "postgres://flag",
		"-r", "http://flag.local",
	})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if cfg.RunAddress != "localhost:8000" {
		t.Fatalf("unexpected run address %q", cfg.RunAddress)
	}
	if cfg.DatabaseURI != "postgres://flag" {
		t.Fatalf("unexpected database URI %q", cfg.DatabaseURI)
	}
	if cfg.AccrualSystemAddress != "http://flag.local" {
		t.Fatalf("unexpected accrual address %q", cfg.AccrualSystemAddress)
	}
	if cfg.AuthSecret != "test-secret" {
		t.Fatalf("unexpected auth secret %q", cfg.AuthSecret)
	}
}

func TestParseSupportsAutotestFlags(t *testing.T) {
	t.Setenv("RUN_ADDRESS", "")
	t.Setenv("DATABASE_URI", "")
	t.Setenv("ACCRUAL_SYSTEM_ADDRESS", "")
	t.Setenv("AUTH_SECRET", "test-secret")

	temporaryDirectory := t.TempDir()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(temporaryDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	cfg, err := Parse([]string{
		"-a", "localhost:8080",
		"-d", "postgresql://postgres:postgres@postgres/praktikum?sslmode=disable",
		"-r", "http://localhost:8081",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.RunAddress != "localhost:8080" ||
		cfg.DatabaseURI != "postgresql://postgres:postgres@postgres/praktikum?sslmode=disable" ||
		cfg.AccrualSystemAddress != "http://localhost:8081" {
		t.Fatalf("autotest flags parsed incorrectly: %#v", cfg)
	}
}

func TestValidateRequiresServiceAddresses(t *testing.T) {
	cfg := Config{RunAddress: "localhost:8080", AuthSecret: "secret"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestParseRequiresExplicitAuthenticationSecret(t *testing.T) {
	t.Chdir(t.TempDir()) // A developer's .env must not supply the missing secret.
	for _, secret := range []string{"", "   ", "configured-secret"} {
		t.Run(secret, func(t *testing.T) {
			t.Setenv("AUTH_SECRET", secret)
			cfg, err := Parse([]string{"-a", "localhost:8080", "-d", "postgres://database", "-r", "http://accrual.local"})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AuthSecret != strings.TrimSpace(secret) {
				t.Fatal("secret was generated or changed")
			}
			err = cfg.Validate()
			if strings.TrimSpace(secret) == "" {
				if err == nil || !strings.Contains(err.Error(), "authentication secret is required") {
					t.Fatalf("expected missing secret error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateRejectsMalformedAddresses(t *testing.T) {
	cfg := Config{
		RunAddress:           "missing-port",
		DatabaseURI:          "postgres://database",
		AccrualSystemAddress: "not-a-url",
		AuthSecret:           strings.Repeat("x", 32),
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected malformed address validation error")
	}
}

func TestParseReadsDotEnvWithoutOverridingOperatingSystemEnvironment(t *testing.T) {
	temporaryDirectory := t.TempDir()
	dotEnv := strings.Join([]string{
		"RUN_ADDRESS=localhost:9100",
		"DATABASE_URI=postgres://dotenv",
		"ACCRUAL_SYSTEM_ADDRESS=http://dotenv-accrual.local/",
		"AUTH_SECRET=dotenv-secret",
	}, "\n")
	if err := os.WriteFile(filepath.Join(temporaryDirectory, ".env"), []byte(dotEnv), 0o600); err != nil {
		t.Fatal(err)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(temporaryDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	t.Setenv("RUN_ADDRESS", "127.0.0.1:9200")
	t.Setenv("DATABASE_URI", "")
	t.Setenv("ACCRUAL_SYSTEM_ADDRESS", "")
	t.Setenv("AUTH_SECRET", "")

	cfg, err := Parse([]string{
		"-a", "localhost:9000",
		"-d", "postgres://flag",
		"-r", "http://flag-accrual.local",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.RunAddress != "localhost:9000" ||
		cfg.DatabaseURI != "postgres://flag" ||
		cfg.AccrualSystemAddress != "http://flag-accrual.local" ||
		cfg.AuthSecret != "dotenv-secret" {
		t.Fatalf("unexpected .env configuration: %#v", cfg)
	}
}

func TestCookieSecure(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "true")
	cfg, err := Parse(nil)
	if err != nil || !cfg.CookieSecure {
		t.Fatalf("environment: %+v, %v", cfg, err)
	}
	cfg, err = Parse([]string{"-cookie-secure=false"})
	if err != nil || cfg.CookieSecure {
		t.Fatalf("flag: %+v, %v", cfg, err)
	}
	t.Setenv("COOKIE_SECURE", "invalid")
	if _, err := Parse(nil); err == nil {
		t.Fatal("accepted invalid boolean")
	}
}

func TestEnvironmentUsedWhenFlagAbsent(t *testing.T) {
	t.Setenv("RUN_ADDRESS", "localhost:9123")
	cfg, err := Parse(nil)
	if err != nil || cfg.RunAddress != "localhost:9123" {
		t.Fatalf("%+v, %v", cfg, err)
	}
}
