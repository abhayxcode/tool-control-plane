package controlplane

import (
	"fmt"
	"os"
	"strings"
)

const (
	SecretRefEnvPrefix  = "env:"
	SecretRefFilePrefix = "file:"
)

type SecretBroker interface {
	ResolveSecret(ref string) (string, error)
}

type LocalSecretBroker struct{}

func (LocalSecretBroker) ResolveSecret(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if strings.HasPrefix(ref, SecretRefEnvPrefix) {
		key := strings.TrimSpace(strings.TrimPrefix(ref, SecretRefEnvPrefix))
		if key == "" {
			return "", fmt.Errorf("secret env ref must include a variable name")
		}
		return os.Getenv(key), nil
	}
	if strings.HasPrefix(ref, SecretRefFilePrefix) {
		path := strings.TrimSpace(strings.TrimPrefix(ref, SecretRefFilePrefix))
		if path == "" {
			return "", fmt.Errorf("secret file ref must include a path")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read secret file %s: %w", path, err)
		}
		return strings.TrimRight(string(content), "\r\n"), nil
	}
	return "", fmt.Errorf("unsupported secret ref %q; expected env:NAME or file:/path", ref)
}

type StaticSecretBroker map[string]string

func (b StaticSecretBroker) ResolveSecret(ref string) (string, error) {
	value, ok := b[strings.TrimSpace(ref)]
	if !ok {
		return "", fmt.Errorf("secret ref %q was not found", ref)
	}
	return value, nil
}
