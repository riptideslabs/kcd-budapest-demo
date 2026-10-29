package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ca is a per-pod CA that lives only in this process's memory. The app trusts its
// certificate (written to a shared emptyDir); the private key never touches disk.
type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

func newCA() (*ca, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "egress-proxy CA for " + host},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	return &ca{cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

func (c *ca) writeCert(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw}), 0o644)
}

func (c *ca) leaf(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if l, ok := c.leaves[host]; ok && time.Until(l.Leaf.NotAfter) > time.Hour {
		return l, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}

	l := &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: key}
	if l.Leaf, err = x509.ParseCertificate(der); err != nil {
		return nil, err
	}

	c.leaves[host] = l

	return l, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))

	return n
}

// proxy accepts CONNECT for the hosts of its configured providers, terminates TLS
// with a certificate from the pod's CA, and forwards each request upstream with
// that provider's token added. Every other destination is refused.
type proxy struct {
	ca     *ca
	routes map[string]*route
}

// route is one provider with its SVID source and current token.
type route struct {
	p     *provider
	creds *credentials
	svids *svidLogger
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "egress-proxy: only HTTPS (CONNECT) is supported", http.StatusMethodNotAllowed)

		return
	}

	host, port, err := net.SplitHostPort(r.Host)
	rt, ok := p.routes[host]

	if err != nil || !ok || port != "443" {
		logf("CONNECT %s refused: not an allowed destination", r.Host)
		http.Error(w, "egress-proxy: destination not allowed", http.StatusForbidden)

		return
	}

	cert, err := p.ca.leaf(host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)

		return
	}

	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}

	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()

		return
	}

	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{"http/1.1"},
		MinVersion:   tls.VersionTLS12,
	})

	// Serve HTTP/1.1 on the decrypted connection until the client closes it.
	ln := newConnListener(tlsConn)
	srv := &http.Server{
		Handler:           rt.forwarder(),
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				ln.Close()
			}
		},
	}
	_ = srv.Serve(ln)
}

var upstream = &http.Transport{
	Proxy:               nil,
	ForceAttemptHTTP2:   true,
	TLSHandshakeTimeout: 10 * time.Second,
	IdleConnTimeout:     90 * time.Second,
	MaxIdleConns:        10,
}

type tokenKey struct{}

func (rt *route) forwarder() http.Handler {
	host := rt.p.host
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = host
			pr.Out.Host = host
			for _, h := range rt.p.strip {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("Authorization", "Bearer "+pr.In.Context().Value(tokenKey{}).(string))
		},
		Transport:     upstream,
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logf("upstream error: %v", err)
			writeError(w, http.StatusBadGateway, "api_error", "egress-proxy: upstream error: "+err.Error())
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		target := fmt.Sprintf("%s https://%s%s", r.Method, host, r.URL.Path)

		tok, err := rt.creds.Get(r.Context())
		if err != nil {
			logf("%s → refused: no %s token for %s (%s)", target, rt.p.name, rt.svids.Subject(), describe(err))
			writeError(w, http.StatusForbidden, "permission_error",
				fmt.Sprintf("egress-proxy: %s did not issue a token to %s (%s)", rt.p.name, rt.svids.Subject(), describe(err)))

			return
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		rp.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), tokenKey{}, tok)))
		logf("%s → added token %s for %s → %d (%s)", target, redact(tok), rt.svids.Subject(), rec.status,
			time.Since(start).Round(time.Millisecond))
	})
}

func writeError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Same shape as the Anthropic API's own errors; OpenAI clients read error.message too.
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	body.Type, body.Error.Type, body.Error.Message = "error", typ, msg
	_ = json.NewEncoder(w).Encode(body)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// connListener hands one connection to http.Server and then blocks until closed.
type connListener struct {
	conn net.Conn
	once sync.Once
	ch   chan net.Conn
	done chan struct{}
}

func newConnListener(c net.Conn) *connListener {
	l := &connListener{conn: c, ch: make(chan net.Conn, 1), done: make(chan struct{})}
	l.ch <- c

	return l
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error {
	l.once.Do(func() { close(l.done) })

	return nil
}

func (l *connListener) Addr() net.Addr { return l.conn.LocalAddr() }
