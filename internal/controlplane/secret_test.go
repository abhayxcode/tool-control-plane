package controlplane

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalSecretBrokerResolvesEnvAndFileRefs(t *testing.T) {
	broker := LocalSecretBroker{}
	t.Setenv("TCP_TEST_SECRET", "env-secret")

	value, err := broker.ResolveSecret("env:TCP_TEST_SECRET")
	if err != nil {
		t.Fatalf("resolve env secret: %v", err)
	}
	if value != "env-secret" {
		t.Fatalf("unexpected env secret value: %q", value)
	}

	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	value, err = broker.ResolveSecret("file:" + path)
	if err != nil {
		t.Fatalf("resolve file secret: %v", err)
	}
	if value != "file-secret" {
		t.Fatalf("unexpected file secret value: %q", value)
	}
}

func TestLocalSecretBrokerRejectsInvalidRefs(t *testing.T) {
	broker := LocalSecretBroker{}
	for _, ref := range []string{"vault:missing", "env:", "file:"} {
		if _, err := broker.ResolveSecret(ref); err == nil {
			t.Fatalf("expected invalid ref %q to fail", ref)
		}
	}
}

func TestStaticSecretBrokerResolvesConfiguredRefs(t *testing.T) {
	broker := StaticSecretBroker{
		"vault:github-token": "resolved-token",
	}
	value, err := broker.ResolveSecret("vault:github-token")
	if err != nil {
		t.Fatalf("resolve static secret: %v", err)
	}
	if value != "resolved-token" {
		t.Fatalf("unexpected static secret value: %q", value)
	}
	if _, err := broker.ResolveSecret("vault:missing"); err == nil {
		t.Fatalf("expected missing static secret to fail")
	}
}
