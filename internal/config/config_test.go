package config

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: myself-ai, Property 6: 超时边界强制
func TestProperty6_TimeoutBoundEnforced(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxTimeout := rapid.IntRange(1, 7200).Draw(t, "max_timeout")
		timeout := rapid.IntRange(maxTimeout+1, maxTimeout+10000).Draw(t, "timeout")

		cfg := defaultConfig()
		cfg.Channel = "feishu"
		cfg.Feishu.AppID = "test-app-id"
		cfg.Feishu.AppSecret = "test-app-secret"
		cfg.Claude.TimeoutSeconds = timeout
		cfg.Claude.MaxTimeoutSeconds = maxTimeout

		err := cfg.Validate()
		if err == nil {
			t.Fatalf("expected Validate() to return error when timeout_seconds(%d) > max_timeout_seconds(%d), got nil",
				timeout, maxTimeout)
		}
	})
}

func TestValidate_TimeoutWithinBounds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxTimeout := rapid.IntRange(1, 7200).Draw(t, "max_timeout")
		timeout := rapid.IntRange(1, maxTimeout).Draw(t, "timeout")

		cfg := defaultConfig()
		cfg.Channel = "feishu"
		cfg.Feishu.AppID = "test-app-id"
		cfg.Feishu.AppSecret = "test-app-secret"
		cfg.Claude.TimeoutSeconds = timeout
		cfg.Claude.MaxTimeoutSeconds = maxTimeout

		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected no error when timeout(%d) <= max_timeout(%d), got: %v", timeout, maxTimeout, err)
		}
	})
}

func TestValidate_MissingAppID(t *testing.T) {
	cfg := defaultConfig()
	cfg.Channel = "feishu"
	cfg.Feishu.AppSecret = "secret"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing AppID")
	}
}

func TestValidate_MissingAppSecret(t *testing.T) {
	cfg := defaultConfig()
	cfg.Channel = "feishu"
	cfg.Feishu.AppID = "id"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing AppSecret")
	}
}
