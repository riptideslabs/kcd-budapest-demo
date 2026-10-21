package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"go.riptides.io/tokenex/pkg/credential"
	"go.riptides.io/tokenex/pkg/option"
	"go.riptides.io/tokenex/pkg/rfc7523"
	"go.riptides.io/tokenex/pkg/token"
)

// svidLogger wraps the SPIFFE token provider so every SVID tokenex presents shows up in the log.
type svidLogger struct {
	inner token.IdentityTokenProvider

	mu      sync.Mutex
	subject string
}

func (s *svidLogger) GetToken(ctx context.Context, opts ...option.Option) (credential.Token, error) {
	tok, err := s.inner.GetToken(ctx, opts...)
	if err != nil {
		logf("SPIRE: no JWT-SVID: %v", err)

		return tok, err
	}

	var claims struct {
		Sub string `json:"sub"`
		Aud any    `json:"aud"`
	}

	if parts := strings.Split(tok.Token, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			_ = json.Unmarshal(payload, &claims)
		}
	}

	s.mu.Lock()
	s.subject = claims.Sub
	s.mu.Unlock()

	logf("SPIRE: JWT-SVID for %s, aud=%v, expires in %s", claims.Sub, claims.Aud, until(tok.ExpiresAt))

	return tok, nil
}

// Subject returns the SPIFFE ID of the last SVID fetched.
func (s *svidLogger) Subject() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subject == "" {
		return "this pod"
	}

	return s.subject
}

// credentials keeps a current Anthropic access token. tokenex refreshes it before expiry;
// when tokenex reports an error (its channel then closes) we back off and start over,
// or retry right away when a request is waiting for a token.
type credentials struct {
	idp token.IdentityTokenProvider
	wif rfc7523.AnthropicWIFConfig

	mu      sync.Mutex
	access  string
	expiry  time.Time
	lastErr error
	refresh string // tokenex's "next refresh" line, held until the token it belongs to is logged
	logged  bool
	changed chan struct{} // closed and replaced on every result
	kick    chan struct{}
}

func newCredentials(idp token.IdentityTokenProvider, wif rfc7523.AnthropicWIFConfig) *credentials {
	return &credentials{
		idp:     idp,
		wif:     wif,
		changed: make(chan struct{}),
		kick:    make(chan struct{}, 1),
	}
}

func (c *credentials) run(ctx context.Context) {
	backoff := 2 * time.Second

	for ctx.Err() == nil {
		var (
			ch  <-chan rfc7523.Credential
			err = errMissingIDs
		)

		if c.wif.FederationRuleID != "" && c.wif.OrganizationID != "" && c.wif.ServiceAccountID != "" {
			ch, err = rfc7523.GetAnthropicCredentials(ctx, logr.New(&tokenexSink{creds: c}), c.idp, c.wif)
		}

		if err != nil {
			c.fail(err)
		} else {
			for res := range ch {
				if res.Err != nil {
					c.fail(res.Err)

					break
				}

				if tok, ok := res.Credential.(*credential.Oauth2Creds); ok {
					c.set(tok.AccessToken, tok.Expiry)
					backoff = 2 * time.Second
				}
			}
		}

		// A request that arrived during the failed attempt already got this result.
		select {
		case <-c.kick:
		default:
		}

		// A denied exchange won't succeed on its own, so only retry it for a request.
		var retry <-chan time.Time
		if !c.denied() {
			retry = time.After(backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-retry:
			backoff = min(backoff*2, time.Minute)
		}
	}
}

func (c *credentials) denied() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return errors.Is(c.lastErr, errMissingIDs) || (c.lastErr != nil && strings.Contains(c.lastErr.Error(), "status 4"))
}

func (c *credentials) set(access string, expiry time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.access, c.expiry, c.lastErr = access, expiry, nil
	close(c.changed)
	c.changed = make(chan struct{})

	logf("tokenex: got Anthropic access token %s, expires in %s", redact(access), until(expiry))

	// Either print the refresh line that arrived first, or let it print when it arrives.
	if c.refresh != "" {
		logf("%s", c.refresh)
		c.refresh = ""
		c.logged = false
	} else {
		c.logged = true
	}
}

// refreshScheduled logs tokenex's refresh plan after the token it belongs to;
// the two arrive on different goroutines.
func (c *credentials) refreshScheduled(in time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	line := fmt.Sprintf("tokenex: next refresh in %s", in.Round(time.Second))
	if c.logged {
		logf("%s", line)
		c.logged = false
	} else {
		c.refresh = line
	}
}

func (c *credentials) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastErr = err
	close(c.changed)
	c.changed = make(chan struct{})

	logf("tokenex: token exchange failed: %s", describe(err))
}

func (c *credentials) valid() (string, bool) {
	if c.access != "" && time.Until(c.expiry) > 5*time.Second {
		return c.access, true
	}

	return "", false
}

// Get returns a valid access token. Without one it asks for a new exchange and
// returns that attempt's result.
func (c *credentials) Get(ctx context.Context) (string, error) {
	c.mu.Lock()
	if tok, ok := c.valid(); ok {
		c.mu.Unlock()

		return tok, nil
	}
	changed := c.changed
	c.mu.Unlock()

	select {
	case c.kick <- struct{}{}:
	default:
	}

	select {
	case <-changed:
	case <-time.After(15 * time.Second):
		return "", errors.New("timed out waiting for a token exchange")
	case <-ctx.Done():
		return "", ctx.Err()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if tok, ok := c.valid(); ok {
		return tok, nil
	}

	if c.lastErr == nil {
		return "", errors.New("no valid token")
	}

	return "", c.lastErr
}

var errMissingIDs = errors.New("ANTHROPIC_FEDERATION_RULE_ID, ANTHROPIC_ORGANIZATION_ID and ANTHROPIC_SERVICE_ACCOUNT_ID are not set")

var apiMessage = regexp.MustCompile(`"message"\s*:\s*"([^"]*)"`)

// describe shortens tokenex's error for the log; Anthropic answers every denied
// exchange with the same opaque 401.
func describe(err error) string {
	msg := err.Error()
	if m := apiMessage.FindStringSubmatch(msg); m != nil && strings.Contains(msg, "status 401") {
		return "401 " + m[1]
	}

	return msg
}

func redact(tok string) string {
	if len(tok) < 20 {
		return "(short token)"
	}

	return tok[:13] + "…" + tok[len(tok)-4:]
}

func until(t time.Time) time.Duration {
	return time.Until(t).Round(time.Second)
}

// tokenexSink passes tokenex's refresh scheduling to the proxy log.
type tokenexSink struct{ creds *credentials }

func (s *tokenexSink) Init(logr.RuntimeInfo)  {}
func (s *tokenexSink) Enabled(level int) bool { return level <= 1 }

func (s *tokenexSink) Info(_ int, msg string, kv ...any) {
	if msg != "scheduling token refresh" {
		return
	}

	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == "refreshIn" {
			if d, ok := kv[i+1].(time.Duration); ok {
				s.creds.refreshScheduled(d)
			}
		}
	}
}

func (s *tokenexSink) Error(err error, msg string, _ ...any) {
	logf("tokenex: %s: %v", msg, err)
}

func (s *tokenexSink) WithValues(...any) logr.LogSink { return s }

func (s *tokenexSink) WithName(string) logr.LogSink { return s }
