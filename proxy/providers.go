package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"

	"go.riptides.io/tokenex/pkg/credential"
	"go.riptides.io/tokenex/pkg/rfc7523"
	"go.riptides.io/tokenex/pkg/rfc8693"
	"go.riptides.io/tokenex/pkg/token"
)

// provider is an API the proxy adds credentials for: the host it intercepts,
// the audience of the SVID it presents, and how tokenex exchanges that SVID.
type provider struct {
	name     string
	host     string
	audience string
	strip    []string // headers the app may send that the token replaces
	missing  error    // set when a required ID is absent
	exchange func(context.Context, logr.Logger, token.IdentityTokenProvider) (<-chan credential.Result, error)
}

var errNotConfigured = errors.New("not configured")

// loadProviders returns every provider that has a setting in the environment.
// The IDs come from the namespace's ConfigMap, so which pod gets which
// credential is configuration, not code.
func loadProviders() []provider {
	var ps []provider

	if anyEnv("ANTHROPIC_") {
		wif := rfc7523.AnthropicWIFConfig{
			FederationRuleID: os.Getenv("ANTHROPIC_FEDERATION_RULE_ID"),
			OrganizationID:   os.Getenv("ANTHROPIC_ORGANIZATION_ID"),
			ServiceAccountID: os.Getenv("ANTHROPIC_SERVICE_ACCOUNT_ID"),
			WorkspaceID:      os.Getenv("ANTHROPIC_WORKSPACE_ID"),
		}
		ps = append(ps, provider{
			name:     "Anthropic",
			host:     "api.anthropic.com",
			audience: envOr("ANTHROPIC_AUDIENCE", "https://api.anthropic.com"),
			strip:    []string{"X-Api-Key"},
			missing:  requireEnv("ANTHROPIC_FEDERATION_RULE_ID", "ANTHROPIC_ORGANIZATION_ID", "ANTHROPIC_SERVICE_ACCOUNT_ID"),
			exchange: func(ctx context.Context, log logr.Logger, idp token.IdentityTokenProvider) (<-chan credential.Result, error) {
				return rfc7523.GetAnthropicCredentials(ctx, log, idp, wif)
			},
		})
	}

	if anyEnv("OPENAI_") {
		wif := rfc8693.OpenAIWIFConfig{
			ClientID:           os.Getenv("OPENAI_CLIENT_ID"),
			IdentityProviderID: os.Getenv("OPENAI_IDENTITY_PROVIDER_ID"),
			ServiceAccountID:   os.Getenv("OPENAI_SERVICE_ACCOUNT_ID"),
		}
		ps = append(ps, provider{
			name:     "OpenAI",
			host:     "api.openai.com",
			audience: envOr("OPENAI_AUDIENCE", "https://api.openai.com"),
			missing:  requireEnv("OPENAI_IDENTITY_PROVIDER_ID", "OPENAI_SERVICE_ACCOUNT_ID"),
			exchange: func(ctx context.Context, log logr.Logger, idp token.IdentityTokenProvider) (<-chan credential.Result, error) {
				return rfc8693.GetOpenAICredentials(ctx, log, idp, wif)
			},
		})
	}

	return ps
}

// anyEnv reports whether a non-empty variable with this prefix is set.
func anyEnv(prefix string) bool {
	for _, kv := range os.Environ() {
		if k, v, _ := strings.Cut(kv, "="); strings.HasPrefix(k, prefix) && strings.TrimSpace(v) != "" {
			return true
		}
	}

	return false
}

func requireEnv(keys ...string) error {
	var unset []string

	for _, k := range keys {
		if strings.TrimSpace(os.Getenv(k)) == "" {
			unset = append(unset, k)
		}
	}

	if len(unset) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s not set", errNotConfigured, strings.Join(unset, ", "))
}
