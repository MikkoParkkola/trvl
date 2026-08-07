package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

// textContent returns a single-element text content block slice.
func textContent(s string) []ContentBlock {
	return []ContentBlock{{Type: "text", Text: s}}
}

// providerHandler is a tool handler that also receives the provider registry
// and the provider runtime.
type providerHandler func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc, reg *providers.Registry, rt *providers.Runtime) ([]ContentBlock, interface{}, error)

// wrapProviderHandler adapts a providerHandler into a ToolHandler by injecting
// the server's provider registry and runtime.
func (s *Server) wrapProviderHandler(handler providerHandler) ToolHandler {
	return func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
		if s.providerRegistry == nil {
			return nil, nil, fmt.Errorf("provider registry not available")
		}
		return handler(ctx, args, elicit, sampling, progress, s.providerRegistry, s.providerRuntime)
	}
}

// --- configure_provider ---

// configureProviderTool returns the MCP tool definition for configure_provider.
func configureProviderTool() ToolDef {
	return ToolDef{
		Name:        "configure_provider",
		Title:       "Configure External Provider",
		Description: "Provider definitions ship as reviewed JSON in the trvl binary. Runtime custom definitions are refused; contribute a definition by pull request or use a fork.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"id":                 {Type: "string", Description: "Unique identifier for this provider (e.g. \"agoda-hotels\")."},
				"name":               {Type: "string", Description: "Human-readable provider name (e.g. \"Agoda\")."},
				"category":           {Type: "string", Description: "Provider category: hotels, flights, ground, restaurants, or reviews."},
				"endpoint":           {Type: "string", Description: "Full URL of the provider's search endpoint."},
				"method":             {Type: "string", Description: "HTTP method (default: POST)."},
				"headers":            {Type: "object", Description: "Extra HTTP headers as key-value pairs."},
				"query_params":       {Type: "object", Description: "URL query parameters as key-value pairs."},
				"body_template":      {Type: "string", Description: "Request body template with {{placeholder}} variables."},
				"auth_type":          {Type: "string", Description: "Authentication type: none, header, or preflight."},
				"auth_preflight_url": {Type: "string", Description: "URL for preflight auth request (when auth_type=preflight)."},
				"auth_extractions":   {Type: "object", Description: "Map of extraction name to {pattern, variable, header} for preflight auth."},
				"results_path":       {Type: "string", Description: "JSONPath to the results array in the response (e.g. \"$.data.results\")."},
				"field_mapping":      {Type: "object", Description: "Map of trvl field name to JSONPath in the provider response."},
				"rate_limit_rps":     {Type: "number", Description: "Maximum requests per second (default: 0.5)."},
				"tls_fingerprint":    {Type: "string", Description: "TLS fingerprint profile (default: chrome)."},
				"cookies_source":     {Type: "string", Description: "Cookie source strategy (default: preflight)."},
			},
			Required: []string{"id", "name", "category", "endpoint", "results_path", "field_mapping"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":              schemaString(),
				"name":            schemaString(),
				"category":        schemaString(),
				"endpoint":        schemaString(),
				"method":          schemaString(),
				"results_path":    schemaString(),
				"field_mapping":   schemaObject(),
				"rate_limit_rps":  schemaNum(),
				"tls_fingerprint": schemaString(),
				"consent": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"granted":   schemaBool(),
						"timestamp": schemaString(),
						"domain":    schemaString(),
					},
				},
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Configure External Provider",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  true,
		},
	}
}

// handleConfigureProvider processes a configure_provider tool call.
func handleConfigureProvider(ctx context.Context, args map[string]any, elicit ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	if reg != nil && reg.IsSourceOnly() {
		id := argString(args, "id")
		message := "Runtime custom provider definitions are disabled. Provider JSON is source code: add a reviewed file under internal/providers/definitions and contribute it by pull request, or use a fork."
		if id != "" {
			message = fmt.Sprintf("Provider %q was not configured. %s", id, message)
		}
		// AN ERROR, not a successful result carrying an apology. The call did
		// not do what it was asked to do, and the caller is usually an agent
		// that branches on the error and never reads the text -- so returning
		// nil here reports "configured" for a provider that will never run.
		// Combined with documentation that still teaches this tool as the way
		// to add a provider, that is a silent no-op presented as success.
		//
		// The explanatory text stays IN the error, because the reason is the
		// actionable part: there is a way to add a provider, it is just not
		// this one.
		//
		// Raised by adversarial review of #587: the trust boundary is closed
		// correctly, but the operator-visible half was wrong.
		return nil, nil, fmt.Errorf("configure_provider: %s", message)
	}
	config, err := parseProviderConfig(args)
	if err != nil {
		return nil, nil, fmt.Errorf("configure_provider: %w", err)
	}

	// Apply sensible defaults before validation.
	if config.RateLimit.RequestsPerSecond == 0 {
		config.RateLimit.RequestsPerSecond = 0.5
	}
	if config.Method == "" {
		config.Method = "POST"
	}
	if config.TLS.Fingerprint == "" {
		config.TLS.Fingerprint = "chrome"
	}
	if config.Cookies.Source == "" {
		config.Cookies.Source = "preflight"
	}

	if err := config.Validate(); err != nil {
		return nil, nil, fmt.Errorf("configure_provider: %w", err)
	}

	// Extract domain from endpoint for display.
	domain := extractDomain(config.Endpoint)

	// Elicitation: ask user for consent.
	if elicit == nil {
		return textContent(
			"Cannot configure provider without user consent.\n\n" +
				"The client does not support elicitation (direct user prompts). " +
				"Please instruct the user to run:\n\n" +
				"  trvl provider add " + config.ID + "\n\n" +
				"from the CLI to configure this provider interactively.",
		), nil, nil
	}

	// Look up ToS URL from the catalog.
	tosURL := ""
	for _, p := range availableProviders {
		if strings.EqualFold(p.Name, config.Name) || p.ID == config.ID {
			tosURL = p.TosURL
			break
		}
	}

	tosLine := ""
	if tosURL != "" {
		tosLine = fmt.Sprintf("\n\n**Terms of Service:** %s", tosURL)
	}

	// A provider config can name a second host in auth_preflight_url, which
	// trvl contacts before the endpoint -- and, on the browser escape hatch
	// below, opens in the user's own browser with their cookies. A consent
	// prompt that names only the endpoint asks the user to approve one address
	// while a second travels with it unseen, so name it. Named even when it is
	// the same host as the endpoint: the fact being consented to is "trvl also
	// contacts this before searching", not "this is somewhere else".
	preflightLine := ""
	if config.Auth != nil && config.Auth.PreflightURL != "" {
		preflightLine = fmt.Sprintf("\n- Contact `%s` first to obtain a session or token", extractDomain(config.Auth.PreflightURL))
	}

	consentMsg := fmt.Sprintf(
		"**Configure external provider: %s**\n\n"+
			"trvl wants to connect to `%s` for %s search.\n\n"+
			"This service may restrict automated access in its Terms of Service.%s\n\n"+
			"**What trvl will do:**\n"+
			"- Send search queries to %s on your behalf%s\n"+
			"- Rate-limit requests to %.1f/sec\n"+
			"- Cache responses locally under ~/.trvl/\n\n"+
			"**What trvl will NOT do:**\n"+
			"- Access your account or private data\n"+
			"- Store credentials beyond this session\n"+
			"- Make purchases or bookings automatically\n\n"+
			"Do you want to enable this provider?",
		config.Name, domain, config.Category, tosLine, domain, preflightLine, config.RateLimit.RequestsPerSecond,
	)

	consentSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"enable": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"yes", "no"},
				"description": "Yes = I accept responsibility for compliance with this service's Terms of Service",
			},
		},
		"required": []string{"enable"},
	}

	result, err := elicit(consentMsg, consentSchema)
	if err != nil {
		// Distinguish timeout from other errors for actionable messaging.
		errMsg := err.Error()
		if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
			return textContent(
				"Provider setup timed out waiting for user response.\n\n" +
					"Please try again — the consent prompt requires a response within the client's timeout window.",
			), nil, nil
		}
		return nil, nil, fmt.Errorf("configure_provider: elicitation failed: %w", err)
	}

	if result == nil {
		return textContent("Provider not enabled: user declined or dismissed the prompt."), nil, nil
	}

	enableVal, _ := result["enable"].(string)
	if enableVal != "yes" {
		return textContent("Provider not enabled: user chose not to enable " + config.Name + "."), nil, nil
	}

	// Record consent.
	config.Consent = &providers.ConsentRecord{
		Granted:   true,
		Timestamp: time.Now(),
		Domain:    domain,
	}

	// Save to registry.
	if err := reg.Save(config); err != nil {
		return nil, nil, fmt.Errorf("configure_provider: save: %w", err)
	}

	// Proactive cookie warming: if the provider uses browser_escape_hatch, ask the
	// platform to open the preflight URL now so cookies are warm before the first
	// search. This is a one-time setup action.
	warmingNote := ""
	if config.Auth != nil && config.Auth.BrowserEscapeHatch && config.Auth.PreflightURL != "" {
		if consent.CookiesDeclined() {
			// This opens the user's REAL browser, on their own profile, for the
			// declared purpose of warming cookies. TRVL_NO_BROWSER_COOKIES is
			// exactly the decline for that, and the elicitation above does not
			// override it: the user consented to enabling a provider, not to
			// having their browser opened and their session reused.
			// Configuration still succeeds — the provider is enabled, it simply
			// starts cold.
			//
			// Defensive rather than a fix for a live bypass: configure_provider
			// never sets BrowserEscapeHatch from its arguments (see the Auth
			// block below), so today this branch is only reachable for a config
			// that acquired the flag elsewhere. It is written now because the
			// flag is one argument away from being settable, and the failure it
			// would cause — a browser opening for a user who declined — is not
			// one to discover in production.
			warmingNote = consent.WarmingNote(consent.WarmingFacts{
				ProviderName: config.Name,
				PreflightURL: config.Auth.PreflightURL,
				Declined:     true,
			})
		} else {
			err := providers.OpenURLInBrowser(config.Auth.PreflightURL, "")
			if err != nil {
				log.Printf("cookie warming: failed to open browser for %s: %v", config.Name, err)
			}
			warmingNote = consent.WarmingNote(consent.WarmingFacts{
				ProviderName: config.Name,
				PreflightURL: config.Auth.PreflightURL,
				LaunchErr:    err,
			})
		}
	}

	summary := fmt.Sprintf("Provider %q enabled for %s search (domain: %s, rate limit: %.1f rps).%s",
		config.Name, config.Category, domain, config.RateLimit.RequestsPerSecond, warmingNote)
	return textContent(summary), newProviderConfigView(config), nil
}

// providerConfigView is what configure_provider returns to the caller.
//
// It exists because ProviderConfig is not safe to hand back. Its Headers,
// QueryParams, BodyTemplate and Auth carry whatever the caller supplied to
// authenticate against the provider -- API keys, bearer tokens, session
// cookies -- and returning the struct serialized every one of them into the
// tool result, from where they reach transcripts, client-side logs and any
// model in the loop. The declared OutputSchema never advertised those fields,
// but the server does not enforce output schemas, so the schema was a
// description of intent and this type is the enforcement.
//
// The rule for adding a field here: it must be something the caller could
// already read off their own request without the secret. Anything derived from
// Headers, QueryParams, BodyTemplate or Auth does not qualify.
type providerConfigView struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Category       string               `json:"category"`
	Endpoint       string               `json:"endpoint"`
	Method         string               `json:"method"`
	ResultsPath    string               `json:"results_path"`
	FieldMapping   map[string]string    `json:"field_mapping,omitempty"`
	RateLimitRPS   float64              `json:"rate_limit_rps"`
	TLSFingerprint string               `json:"tls_fingerprint,omitempty"`
	Consent        *providerConsentView `json:"consent,omitempty"`
}

type providerConsentView struct {
	Granted   bool   `json:"granted"`
	Timestamp string `json:"timestamp,omitempty"`
	Domain    string `json:"domain,omitempty"`
}

// redactedEndpoint returns the endpoint with the two parts that most often
// carry a credential removed: userinfo and the query string.
//
// The endpoint is not itself a secret and the caller of configure_provider
// supplied it in the same call, so this is not about hiding it from them. It is
// about what the value becomes afterwards. A structured MCP result is written
// to the client's transcript and to whatever the host logs, which is a wider
// and longer-lived audience than the one request -- and API keys live in query
// strings ("?apikey=...") and in userinfo ("https://user:pass@host/") often
// enough that echoing them back is a disclosure with no reader who needs it.
//
// Scheme, host and path survive, so the value still answers the question the
// caller asked it: that the endpoint stored is the one they meant. A path can
// carry a secret too (that is exactly what a Slack webhook is), but for a
// provider endpoint the path is the API route and dropping it would leave the
// confirmation useless; the two fields removed here cost nothing to lose.
func redactedEndpoint(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Unparseable or host-less: return nothing rather than guess which part
		// of an unrecognised string was safe.
		return "invalid"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	// The path goes too, not just the query. A secret in a URL path is not an
	// exotic shape -- it is how Telegram, Slack and every "bot/<token>/method"
	// API is addressed -- and keeping the path meant this view still returned a
	// live credential for those providers, having removed the two places a
	// credential is easier to spot. Scheme and host answer the question this
	// view exists for ("which service is this pointed at"); the path only
	// answers "with what key".
	u.Path = ""
	u.RawPath = ""
	u.Opaque = ""
	return u.String()
}

func newProviderConfigView(c *providers.ProviderConfig) *providerConfigView {
	if c == nil {
		return nil
	}
	v := &providerConfigView{
		ID:             c.ID,
		Name:           c.Name,
		Category:       c.Category,
		Endpoint:       redactedEndpoint(c.Endpoint),
		Method:         c.Method,
		ResultsPath:    c.ResponseMapping.ResultsPath,
		FieldMapping:   c.ResponseMapping.Fields,
		RateLimitRPS:   c.RateLimit.RequestsPerSecond,
		TLSFingerprint: c.TLS.Fingerprint,
	}
	if c.Consent != nil {
		v.Consent = &providerConsentView{
			Granted:   c.Consent.Granted,
			Timestamp: c.Consent.Timestamp.UTC().Format(time.RFC3339),
			Domain:    c.Consent.Domain,
		}
	}
	return v
}

// The wording of that note, and the conditions that entitle trvl to each
// sentence of it, live in internal/consent (warming.go). They were free-form
// format strings here, which is where two false statements about the user's own
// machine were introduced by ordinary edits (#528).

// parseProviderConfig extracts a ProviderConfig from MCP tool arguments.
func parseProviderConfig(args map[string]any) (*providers.ProviderConfig, error) {
	// body_template type guard: some LLMs send a JSON object instead of a
	// JSON string for body_template. Auto-stringify it rather than rejecting.
	bodyTemplate := argString(args, "body_template")
	if bodyTemplate == "" {
		if v, ok := args["body_template"]; ok && v != nil {
			if _, isMap := v.(map[string]any); isMap {
				b, err := json.Marshal(v)
				if err == nil {
					bodyTemplate = string(b)
				}
			}
		}
	}

	config := &providers.ProviderConfig{
		ID:           argString(args, "id"),
		Name:         argString(args, "name"),
		Category:     argString(args, "category"),
		Endpoint:     argString(args, "endpoint"),
		Method:       argString(args, "method"),
		BodyTemplate: bodyTemplate,
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: argString(args, "results_path"),
		},
		RateLimit: providers.RateLimitConfig{
			RequestsPerSecond: argFloat(args, "rate_limit_rps", 0),
		},
		TLS: providers.TLSConfig{
			Fingerprint: argString(args, "tls_fingerprint"),
		},
		Cookies: providers.CookieConfig{
			Source: argString(args, "cookies_source"),
		},
	}

	// Build Auth config if auth_type is provided.
	if authType := argString(args, "auth_type"); authType != "" {
		config.Auth = &providers.AuthConfig{
			Type:         authType,
			PreflightURL: argString(args, "auth_preflight_url"),
		}
		if v, ok := args["auth_extractions"]; ok {
			config.Auth.Extractions = parseAuthExtractions(v)
		}
	}

	// Parse headers (map[string]string).
	if v, ok := args["headers"]; ok {
		config.Headers = parseStringMap(v)
	}

	// Parse query_params (map[string]string).
	if v, ok := args["query_params"]; ok {
		config.QueryParams = parseStringMap(v)
	}

	// Parse field_mapping into ResponseMapping.Fields.
	if v, ok := args["field_mapping"]; ok {
		config.ResponseMapping.Fields = parseStringMap(v)
	}

	// Refuse a destination the policy will not dial anyway, here at the seam
	// where a caller hands us the URL, so the answer is a stated refusal
	// rather than a connection error later in a search. The request-time check
	// in internal/providers stays the authority -- this one cannot see what a
	// hostname will resolve to at request time -- but it is the only place
	// that can refuse a non-HTTP scheme, which never reaches a dialer.
	if err := providers.CheckDestinationURL(config.Endpoint); err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	if config.Auth != nil {
		if err := providers.CheckDestinationURL(config.Auth.PreflightURL); err != nil {
			return nil, fmt.Errorf("auth_preflight_url: %w", err)
		}
	}

	return config, nil
}

// parseStringMap converts a map[string]any to map[string]string.
// Also handles a JSON string encoding.
func parseStringMap(v any) map[string]string {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]string, len(val))
		for k, v := range val {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
		return result
	case string:
		if val == "" {
			return nil
		}
		var result map[string]string
		if err := json.Unmarshal([]byte(val), &result); err != nil {
			return nil
		}
		return result
	default:
		return nil
	}
}

// parseAuthExtractions converts a map[string]any to map[string]Extraction.
func parseAuthExtractions(v any) map[string]providers.Extraction {
	m, ok := v.(map[string]any)
	if !ok {
		// Try JSON string.
		if s, ok := v.(string); ok && s != "" {
			var result map[string]providers.Extraction
			if err := json.Unmarshal([]byte(s), &result); err != nil {
				return nil
			}
			return result
		}
		return nil
	}

	result := make(map[string]providers.Extraction, len(m))
	for name, val := range m {
		em, ok := val.(map[string]any)
		if !ok {
			continue
		}
		ext := providers.Extraction{}
		if s, ok := em["pattern"].(string); ok {
			ext.Pattern = s
		}
		if s, ok := em["variable"].(string); ok {
			ext.Variable = s
		}
		if s, ok := em["header"].(string); ok {
			ext.Header = s
		}
		result[name] = ext
	}
	return result
}

// extractDomain returns the hostname from a URL, or the URL itself if parsing fails.
func extractDomain(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		// Fallback: try to extract something useful.
		parts := strings.SplitN(endpoint, "/", 4)
		if len(parts) >= 3 {
			return parts[2]
		}
		return endpoint
	}
	return u.Hostname()
}

// --- list_providers ---

// listProvidersTool returns the MCP tool definition for list_providers.
func listProvidersTool() ToolDef {
	return ToolDef{
		Name:        "list_providers",
		Title:       "List External Providers",
		Description: "List all configured external data providers with their status, consent, and error counts.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"providers": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":           schemaString(),
						"name":         schemaString(),
						"category":     schemaString(),
						"domain":       schemaString(),
						"consent":      schemaBool(),
						"last_success": schemaString(),
						"error_count":  schemaInt(),
					},
				}),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "List External Providers",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleListProviders processes a list_providers tool call.
func handleListProviders(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	// ListSafe returns value copies under RLock so reading the breaker fields
	// (LastSuccess/ErrorCount) here never races MarkSuccess/MarkError running
	// concurrently from an in-flight search (#144 class; MIK-5858).
	configs := reg.ListSafe()

	if len(configs) == 0 {
		return textContent("No reviewed external providers are enabled. Use `trvl providers enable <id>` for a provider shipped with this binary."), nil, nil
	}

	type providerSummary struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Domain      string `json:"domain"`
		Consent     bool   `json:"consent"`
		LastSuccess string `json:"last_success,omitempty"`
		ErrorCount  int    `json:"error_count"`
	}

	summaries := make([]providerSummary, 0, len(configs))
	var lines []string

	for _, c := range configs {
		domain := extractDomain(c.Endpoint)
		lastSuccess := ""
		if !c.LastSuccess.IsZero() {
			lastSuccess = c.LastSuccess.Format(time.RFC3339)
		}

		consentGranted := c.Consent != nil && c.Consent.Granted
		summaries = append(summaries, providerSummary{
			ID:          c.ID,
			Name:        c.Name,
			Category:    c.Category,
			Domain:      domain,
			Consent:     consentGranted,
			LastSuccess: lastSuccess,
			ErrorCount:  c.ErrorCount,
		})

		status := "enabled"
		if !consentGranted {
			status = "no consent"
		}
		line := fmt.Sprintf("- %s (%s) [%s] %s", c.Name, c.Category, status, domain)
		if c.ErrorCount > 0 {
			line += fmt.Sprintf(" (%d errors)", c.ErrorCount)
		}
		lines = append(lines, line)
	}

	summary := fmt.Sprintf("%d provider(s) configured:\n%s", len(configs), strings.Join(lines, "\n"))
	structured := map[string]any{"providers": summaries}
	content, err := buildAnnotatedContentBlocks(summary, structured)
	if err != nil {
		return nil, nil, err
	}
	// Return structured data so programmatic clients can parse it.
	// OutputSchema is intentionally omitted on the tool definition so strict
	// MCP clients don't reject the nested-array shape ("expected record,
	// received array" was previously seen against aggressive validators).
	return content, structured, nil
}

// --- remove_provider ---

// removeProviderTool returns the MCP tool definition for remove_provider.
func removeProviderTool() ToolDef {
	return ToolDef{
		Name:        "remove_provider",
		Title:       "Remove External Provider",
		Description: "Disable a reviewed external provider by ID. Its embedded definition remains immutable in the binary.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"id": {Type: "string", Description: "ID of the provider to remove."},
			},
			Required: []string{"id"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message": schemaString(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Remove External Provider",
			ReadOnlyHint:    false,
			DestructiveHint: true,
			IdempotentHint:  true,
		},
	}
}

// handleRemoveProvider processes a remove_provider tool call.
func handleRemoveProvider(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	id := argString(args, "id")
	if id == "" {
		return nil, nil, fmt.Errorf("remove_provider: id is required")
	}

	if err := reg.Delete(id); err != nil {
		return nil, nil, fmt.Errorf("remove_provider: %w", err)
	}

	return textContent(fmt.Sprintf("Provider %q disabled.", id)), nil, nil
}

// --- test_provider ---
