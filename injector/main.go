// Command egress-injector is a mutating admission webhook that adds the
// egress-proxy sidecar to pods in namespaces labeled egress-proxy=enabled.
//
// It creates its own serving certificate at startup and writes the CA into its
// MutatingWebhookConfiguration, so it needs no cert-manager. Run one replica:
// each replica would make its own CA.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "egress-injector:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctrllog.SetLogger(logr.Discard())

	namespace := envOr("POD_NAMESPACE", "egress-system")
	service := envOr("SERVICE_NAME", "egress-injector")
	webhookConfig := envOr("WEBHOOK_CONFIG", "egress-proxy-injector")

	cert, caPEM, err := servingCert([]string{
		service + "." + namespace + ".svc",
		service + "." + namespace + ".svc.cluster.local",
	})
	if err != nil {
		return err
	}

	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	var ready atomic.Bool

	go func() {
		publishCA(ctx, client, webhookConfig, caPEM)
		ready.Store(true)
	}()

	mux := http.NewServeMux()
	mux.Handle("/mutate", &admission.Webhook{Handler: &injector{
		decoder:   admission.NewDecoder(clientgoscheme.Scheme),
		image:     envOr("PROXY_IMAGE", "egress-proxy:dev"),
		configMap: envOr("WIF_CONFIGMAP", "anthropic-wif"),
		proxyAddr: envOr("PROXY_ADDR", "127.0.0.1:3128"),
	}})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "CA not published yet", http.StatusServiceUnavailable)

			return
		}
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:              envOr("LISTEN_ADDR", ":8443"),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	logf("serving the webhook on %s", srv.Addr)

	if err := srv.ListenAndServeTLS("", ""); err != nil && ctx.Err() == nil {
		return err
	}

	return nil
}

// publishCA writes the CA into every webhook of the configuration, retrying
// until the API server accepts it.
func publishCA(ctx context.Context, client kubernetes.Interface, name string, caPEM []byte) {
	for ctx.Err() == nil {
		err := func() error {
			cfg, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			var ops []map[string]any
			for i := range cfg.Webhooks {
				ops = append(ops, map[string]any{
					"op":    "add",
					"path":  fmt.Sprintf("/webhooks/%d/clientConfig/caBundle", i),
					"value": base64.StdEncoding.EncodeToString(caPEM),
				})
			}

			patch, err := json.Marshal(ops)
			if err != nil {
				return err
			}

			_, err = client.AdmissionregistrationV1().MutatingWebhookConfigurations().Patch(ctx, name, types.JSONPatchType, patch,
				metav1.PatchOptions{FieldManager: "egress-injector"})

			return err
		}()
		if err == nil {
			logf("published the CA to MutatingWebhookConfiguration %s", name)

			return
		}

		logf("publishing the CA to %s: %v (retrying)", name, err)

		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// servingCert makes a throwaway CA and a serving certificate for the webhook's
// Service names. Both live only in this process.
func servingCert(dnsNames []string) (tls.Certificate, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	caTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "egress-injector CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	cert := tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}

	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))

	return n
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
