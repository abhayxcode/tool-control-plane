package controlplane

import (
	"fmt"
	"strings"
	"time"
)

const (
	ConnectorStatusConfigured = "configured"
	ConnectorStatusReady      = "ready"
	ConnectorStatusBlocked    = "blocked"
	ConnectorStatusDisabled   = "disabled"

	ConnectorSourceAPI = "api"
)

type Connector struct {
	ID         string         `json:"id"`
	OrgID      string         `json:"org_id"`
	Name       string         `json:"name,omitempty"`
	Provider   string         `json:"provider"`
	Capability string         `json:"capability"`
	Config     map[string]any `json:"config,omitempty"`
	SecretRef  string         `json:"secret_ref,omitempty"`
	Status     string         `json:"status"`
	Source     string         `json:"source,omitempty"`
	CreatedAt  string         `json:"created_at,omitempty"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
}

type ConnectorCreateRequest struct {
	OrgID      string         `json:"org_id"`
	Name       string         `json:"name,omitempty"`
	Provider   string         `json:"provider"`
	Capability string         `json:"capability"`
	Config     map[string]any `json:"config,omitempty"`
	SecretRef  string         `json:"secret_ref,omitempty"`
	Status     string         `json:"status,omitempty"`
}

func newConnector(req ConnectorCreateRequest, now time.Time) (Connector, error) {
	orgID := strings.TrimSpace(req.OrgID)
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	capability := strings.ToLower(strings.TrimSpace(req.Capability))
	if orgID == "" {
		return Connector{}, fmt.Errorf("org_id is required")
	}
	if provider == "" {
		return Connector{}, fmt.Errorf("provider is required")
	}
	if capability == "" {
		return Connector{}, fmt.Errorf("capability is required")
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status == "" {
		status = ConnectorStatusConfigured
	}
	if !validConnectorStatus(status) {
		return Connector{}, fmt.Errorf("status must be one of configured, ready, blocked, or disabled")
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	return Connector{
		OrgID:      orgID,
		Name:       strings.TrimSpace(req.Name),
		Provider:   provider,
		Capability: capability,
		Config:     redactConnectorConfig(req.Config),
		SecretRef:  strings.TrimSpace(req.SecretRef),
		Status:     status,
		Source:     ConnectorSourceAPI,
		CreatedAt:  timestamp,
		UpdatedAt:  timestamp,
	}, nil
}

func validConnectorStatus(status string) bool {
	switch status {
	case ConnectorStatusConfigured, ConnectorStatusReady, ConnectorStatusBlocked, ConnectorStatusDisabled:
		return true
	default:
		return false
	}
}

func redactConnectorConfig(value map[string]any) map[string]any {
	return redactToolCallMap(value)
}
