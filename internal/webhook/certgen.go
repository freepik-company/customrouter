/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update

// EnsureCerts ensures a self-signed CA and server certificate exist in a shared Secret.
// If the Secret already exists and the certs are valid, they are reused so that all
// replicas share the same TLS identity. Otherwise, new certs are generated and stored.
// Returns the CA PEM for use by the CABundleReconciler.
func EnsureCerts(ctx context.Context, cl client.Client, certDir, webhookConfigName, serviceName, namespace string) ([]byte, error) {
	secretName := serviceName + "-tls"

	dnsNames := []string{
		serviceName + "." + namespace + ".svc",
		serviceName + "." + namespace + ".svc.cluster.local",
	}

	// --- Try to read existing Secret ---
	caPEM, certPEM, keyPEM, err := readCertsFromSecret(ctx, cl, secretName, namespace, dnsNames)
	if err != nil {
		return nil, err
	}

	// --- Generate new certs if needed ---
	if caPEM == nil {
		caPEM, certPEM, keyPEM, err = generateCerts(serviceName, namespace)
		if err != nil {
			return nil, err
		}

		created, err := tryCreateSecret(ctx, cl, secretName, namespace, caPEM, certPEM, keyPEM)
		if err != nil {
			return nil, err
		}

		if !created {
			// Another replica created the Secret first — use their certs
			caPEM, certPEM, keyPEM, err = readCertsFromSecret(ctx, cl, secretName, namespace, dnsNames)
			if err != nil {
				return nil, err
			}
			if caPEM == nil {
				return nil, fmt.Errorf("secret %s/%s exists but contains invalid certs", namespace, secretName)
			}
		}
	}

	// --- Write to disk ---
	if err := writeCertsToDisk(certDir, certPEM, keyPEM); err != nil {
		return nil, err
	}

	// --- Patch ValidatingWebhookConfiguration (with retry on conflict) ---
	if err := patchWebhookConfig(ctx, cl, webhookConfigName, caPEM); err != nil {
		return nil, err
	}

	return caPEM, nil
}

// readCertsFromSecret reads and validates certs from the Secret.
// Returns (nil, nil, nil, nil) if the Secret does not exist or certs are invalid.
func readCertsFromSecret(ctx context.Context, cl client.Client, secretName, namespace string, expectedDNSNames []string) (caPEM, certPEM, keyPEM []byte, err error) {
	var secret corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("getting secret %s/%s: %w", namespace, secretName, err)
	}

	ca := secret.Data["ca.crt"]
	cert := secret.Data["tls.crt"]
	key := secret.Data["tls.key"]

	if certsAreValid(ca, cert, key, expectedDNSNames) {
		return ca, cert, key, nil
	}
	return nil, nil, nil, nil
}

// tryCreateSecret attempts to create the Secret. Returns (true, nil) if created,
// (false, nil) if another replica already created it, or (false, err) on failure.
func tryCreateSecret(ctx context.Context, cl client.Client, secretName, namespace string, caPEM, certPEM, keyPEM []byte) (bool, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"ca.crt":  caPEM,
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}

	if err := cl.Create(ctx, secret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, fmt.Errorf("creating secret %s/%s: %w", namespace, secretName, err)
	}
	return true, nil
}

// certsAreValid checks that the CA and server cert can be parsed,
// the server cert is not expired (with 30-day buffer), and the SANs match.
func certsAreValid(caPEM, certPEM, keyPEM []byte, expectedDNSNames []string) bool {
	if len(caPEM) == 0 || len(certPEM) == 0 || len(keyPEM) == 0 {
		return false
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	// Check expiry with 30-day buffer
	if time.Now().Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		return false
	}

	// Check SANs match
	certDNS := make(map[string]struct{}, len(cert.DNSNames))
	for _, d := range cert.DNSNames {
		certDNS[d] = struct{}{}
	}
	for _, expected := range expectedDNSNames {
		if _, ok := certDNS[expected]; !ok {
			return false
		}
	}

	// Check key can be parsed
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return false
	}
	if _, err := x509.ParseECPrivateKey(keyBlock.Bytes); err != nil {
		return false
	}

	return true
}

func generateCerts(serviceName, namespace string) (caPEM, serverCertPEM, serverKeyPEM []byte, err error) {
	// --- CA ---
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating serial: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "customrouter-webhook-ca",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating CA cert: %w", err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing CA cert: %w", err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	// --- Server cert ---
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating server key: %w", err)
	}

	serverSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating server serial: %w", err)
	}

	dnsNames := []string{
		serviceName + "." + namespace + ".svc",
		serviceName + "." + namespace + ".svc.cluster.local",
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			CommonName: dnsNames[0],
		},
		DNSNames:    dnsNames,
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating server cert: %w", err)
	}
	serverCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertDER})

	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshalling server key: %w", err)
	}
	serverKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER})

	return caPEM, serverCertPEM, serverKeyPEM, nil
}

func writeCertsToDisk(certDir string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return fmt.Errorf("creating cert dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "tls.crt"), certPEM, 0o644); err != nil {
		return fmt.Errorf("writing tls.crt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "tls.key"), keyPEM, 0o600); err != nil {
		return fmt.Errorf("writing tls.key: %w", err)
	}
	return nil
}

func patchWebhookConfig(ctx context.Context, cl client.Client, webhookConfigName string, caPEM []byte) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var webhookConfig admissionregistrationv1.ValidatingWebhookConfiguration
		if err := cl.Get(ctx, types.NamespacedName{Name: webhookConfigName}, &webhookConfig); err != nil {
			return fmt.Errorf("getting webhook config %q: %w", webhookConfigName, err)
		}
		for i := range webhookConfig.Webhooks {
			webhookConfig.Webhooks[i].ClientConfig.CABundle = caPEM
		}
		return cl.Update(ctx, &webhookConfig)
	})
}

// CABundleReconciler periodically ensures the ValidatingWebhookConfiguration
// has the correct CA bundle. This handles Helm upgrades that may wipe the caBundle.
type CABundleReconciler struct {
	Client     client.Client
	ConfigName string
	CaPEM      []byte
	Interval   time.Duration
}

func (r *CABundleReconciler) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = patchWebhookConfig(ctx, r.Client, r.ConfigName, r.CaPEM)
		}
	}
}

func (r *CABundleReconciler) NeedLeaderElection() bool {
	return true
}

// GetNamespace reads the pod namespace from the service account mount.
// Falls back to "default" if not running in-cluster.
func GetNamespace() string {
	ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "default"
	}
	return string(ns)
}
