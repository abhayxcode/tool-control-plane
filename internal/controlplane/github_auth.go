package controlplane

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const githubAppTokenRefreshSkew = 5 * time.Minute

type GitHubTokenSource interface {
	Token() (string, error)
	Configured() bool
	Kind() string
}

type StaticGitHubTokenSource struct {
	TokenValue string
}

func (s StaticGitHubTokenSource) Token() (string, error) {
	token := strings.TrimSpace(s.TokenValue)
	if token == "" {
		return "", errors.New("github adapter requires GITHUB_TOKEN or GitHub App installation credentials")
	}
	return token, nil
}

func (s StaticGitHubTokenSource) Configured() bool {
	return strings.TrimSpace(s.TokenValue) != ""
}

func (s StaticGitHubTokenSource) Kind() string {
	if s.Configured() {
		return "token"
	}
	return "none"
}

type GitHubAppTokenSourceConfig struct {
	AppID          string
	InstallationID string
	PrivateKeyPEM  string
	BaseURL        string
	Client         *http.Client
	Now            func() time.Time
}

type GitHubAppTokenSource struct {
	appID          string
	installationID string
	privateKey     *rsa.PrivateKey
	baseURL        string
	client         *http.Client
	now            func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func NewGitHubAppTokenSource(config GitHubAppTokenSourceConfig) (*GitHubAppTokenSource, error) {
	appID := strings.TrimSpace(config.AppID)
	installationID := strings.TrimSpace(config.InstallationID)
	privateKeyPEM := strings.TrimSpace(config.PrivateKeyPEM)
	if appID == "" || installationID == "" || privateKeyPEM == "" {
		return nil, errors.New("github app auth requires app id, installation id, and private key")
	}
	privateKey, err := parseGitHubAppPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &GitHubAppTokenSource{
		appID:          appID,
		installationID: installationID,
		privateKey:     privateKey,
		baseURL:        baseURL,
		client:         client,
		now:            now,
	}, nil
}

func (s *GitHubAppTokenSource) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && s.now().Add(githubAppTokenRefreshSkew).Before(s.expiresAt) {
		return s.token, nil
	}
	jwt, err := s.jwt()
	if err != nil {
		return "", err
	}
	requestURL := fmt.Sprintf("%s/app/installations/%s/access_tokens", s.baseURL, s.installationID)
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github app installation token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read github app installation token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("github app installation token request returned HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Token) == "" {
		return "", errors.New("github app installation token response did not include token")
	}
	expiresAt := s.now().Add(time.Hour)
	if strings.TrimSpace(parsed.ExpiresAt) != "" {
		if parsedExpiresAt, err := time.Parse(time.RFC3339, parsed.ExpiresAt); err == nil {
			expiresAt = parsedExpiresAt
		}
	}
	s.token = parsed.Token
	s.expiresAt = expiresAt
	return s.token, nil
}

func (s *GitHubAppTokenSource) Configured() bool {
	return s != nil && s.appID != "" && s.installationID != "" && s.privateKey != nil
}

func (s *GitHubAppTokenSource) Kind() string {
	if s.Configured() {
		return "github_app"
	}
	return "none"
}

func (s *GitHubAppTokenSource) jwt() (string, error) {
	now := s.now()
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	claims := map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": s.appID,
	}
	encodedHeader, err := encodeJWTPart(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeJWTPart(claims)
	if err != nil {
		return "", err
	}
	signingInput := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func encodeJWTPart(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func parseGitHubAppPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("github app private key must be PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse github app private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app private key must be RSA")
	}
	return key, nil
}
