// Command egress-proxy is the demo's sidecar. It fetches the pod's JWT-SVID from the
// SPIRE agent, exchanges it with tokenex for a short-lived token from each configured
// provider (Anthropic, OpenAI), and adds that token to HTTPS requests the app sends
// through it (HTTPS_PROXY).
//
//	egress-proxy                  run the proxy
//	egress-proxy svid [audience]  print the pod's JWT-SVID (used by the stage script)
//	egress-proxy healthcheck      exit 0 if the proxy accepts connections
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback" // the image is FROM scratch

	"go.riptides.io/tokenex/pkg/token/spiffe"
)

type config struct {
	listenAddr string
	caOut      string
	providers  []provider
}

func loadConfig() config {
	return config{
		listenAddr: envOr("LISTEN_ADDR", "127.0.0.1:3128"),
		caOut:      envOr("CA_CERT_OUT", "/run/egress-proxy/ca.crt"),
		providers:  loadProviders(),
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
		err = printSVID(cfg, os.Args[2:])
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

	ca, err := newCA()
	if err != nil {
		return err
	}

	if err := ca.writeCert(cfg.caOut); err != nil {
		return err
	}

	routes := map[string]*route{}
	hosts := []string{}

	for i := range cfg.providers {
		p := &cfg.providers[i]

		idp, err := spiffe.NewIdentityTokenProvider(spiffe.WithAudience(p.audience))
		if err != nil {
			return err
		}

		svids := &svidLogger{inner: idp}
		creds := newCredentials(svids, p)
		go creds.run(ctx)

		routes[p.host] = &route{p: p, creds: creds, svids: svids}
		hosts = append(hosts, p.host)
	}

	ln, err := net.Listen("tcp", cfg.listenAddr)
	if err != nil {
		return err
	}

	if len(hosts) == 0 {
		logf("listening on %s, but no provider is configured: set ANTHROPIC_* or OPENAI_* (see anthropic.env.example)", cfg.listenAddr)
	} else {
		logf("listening on %s, adding credentials for %s (CA cert in %s)", cfg.listenAddr, strings.Join(hosts, ", "), cfg.caOut)
	}

	srv := &http.Server{
		Handler:           &proxy{ca: ca, routes: routes},
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

// printSVID prints a JWT-SVID for the given audience, or for the first provider's.
func printSVID(cfg config, args []string) error {
	var audience string

	switch {
	case len(args) > 0:
		audience = args[0]
	case len(cfg.providers) > 0:
		audience = cfg.providers[0].audience
	default:
		return errors.New("no provider configured; pass an audience")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	idp, err := spiffe.NewIdentityTokenProvider(spiffe.WithAudience(audience))
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
