package providers

import "time"

// View is the credential-free, diagnostic representation of a provider. It is
// safe for CLI JSON output: endpoint paths/queries, headers, request bodies,
// auth configuration, consent records, mappings, and stored raw errors are
// deliberately absent.
type View struct {
	SchemaVersion string    `json:"schema_version,omitempty"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Category      string    `json:"category"`
	Domain        string    `json:"domain,omitempty"`
	Status        string    `json:"status"`
	LastSuccess   time.Time `json:"last_success,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at,omitempty"`
	ErrorCount    int       `json:"error_count,omitempty"`
	Version       int       `json:"version"`
	Personal      bool      `json:"personal,omitempty"`
}

// CredentialFreeViews converts configs to the only representation intended
// for terminal, CI, or MCP diagnostic serialization.
func CredentialFreeViews(configs []*ProviderConfig) []View {
	views := make([]View, 0, len(configs))
	for _, config := range configs {
		if config == nil {
			continue
		}
		views = append(views, View{
			SchemaVersion: config.SchemaVersion,
			ID:            config.ID,
			Name:          config.Name,
			Category:      config.Category,
			Domain:        config.EndpointDomain(),
			Status:        config.Status(),
			LastSuccess:   config.LastSuccess,
			LastErrorAt:   config.LastErrorAt,
			ErrorCount:    config.ErrorCount,
			Version:       config.Version,
			Personal:      config.Personal,
		})
	}
	return views
}
