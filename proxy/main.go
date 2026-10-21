// Command egress-proxy is the demo's sidecar. It fetches the pod's JWT-SVID from the
// SPIRE agent, exchanges it for a short-lived Anthropic access token with tokenex,
// and adds that token to HTTPS requests the app sends through it (HTTPS_PROXY).
//
//	egress-proxy              run the proxy
//	egress-proxy svid         print the pod's JWT-SVID (used by the stage script)
//	egress-proxy healthcheck  exit 0 if the proxy accepts connections
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback" // the image is FROM scratch

	"go.riptides.io/tokenex/pkg/rfc7523"
	"go.riptides.io/tokenex/pkg/token/spiffe"
)

type config struct {
	listenAddr string
	injectHost string
	audience   string
	caOut      string
	wif        rfc7523.AnthropicWIFConfig
}

func loadConfig() config {
	return config{
		listenAddr: envOr("LISTEN_ADDR", "127.0.0.1:3128"),
		injectHost: envOr("INJECT_HOST", "api.anthropic.com"),
		audience:   envOr("AUDIENCE", "https://api.anthropic.com"),
		caOut:      envOr("CA_CERT_OUT", "/run/egress-proxy/ca.crt"),
		wif: rfc7523.AnthropicWIFConfig{
			FederationRuleID: os.Getenv("ANTHROPIC_FEDERATION_RULE_ID"),
			OrganizationID:   os.Getenv("ANTHROPIC_ORGANIZATION_ID"),
			ServiceAccountID: os.Getenv("ANTHROPIC_SERVICE_ACCOUNT_ID"),
			WorkspaceID:      os.Getenv("ANTHROPIC_WORKSPACE_ID"),
		},
	}
}

func main() {
	cfg := loadConfig()

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error

	switch cmd {
	case "":
		err = serve(cfg)
	case "svid":
		err = printSVID(cfg)
	case "healthcheck":
		err = healthcheck(cfg)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "egress-proxy:", err)
		os.Exit(1)
	}
}

func serve(cfg config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	idp, err := spiffe.NewIdentityTokenProvider(spiffe.WithAudience(cfg.audience))
	if err != nil {
		return err
	}

	ca, err := newCA()
	if err != nil {
		return err
	}

	if err := ca.writeCert(cfg.caOut); err != nil {
		return err
	}

	svids := &svidLogger{inner: idp}
	creds := newCredentials(svids, cfg.wif)
	go creds.run(ctx)

	ln, err := net.Listen("tcp", cfg.listenAddr)
	if err != nil {
		return err
	}

	logf("listening on %s, adding credentials for %s (CA cert in %s)", cfg.listenAddr, cfg.injectHost, cfg.caOut)

	srv := &http.Server{
		Handler:           &proxy{host: cfg.injectHost, ca: ca, creds: creds, svids: svids},
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.Serve(ln); err != nil && ctx.Err() == nil {
		return err
	}

	return nil
}

func printSVID(cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	idp, err := spiffe.NewIdentityTokenProvider(spiffe.WithAudience(cfg.audience))
	if err != nil {
		return err
	}

	tok, err := idp.GetToken(ctx)
	if err != nil {
		return err
	}

	fmt.Println(tok.Token)

	return nil
}

func healthcheck(cfg config) error {
	conn, err := net.DialTimeout("tcp", cfg.listenAddr, time.Second)
	if err != nil {
		return err
	}

	return conn.Close()
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}

	return def
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
