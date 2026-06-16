package rest

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAccessVerifierVerify(t *testing.T) {
	privateKey, certPEM := generateTestAccessCertificate(t)
	kid := "kid-1"

	var certsHits int
	responseBody := marshalAccessCertsResponse(t, kid, certPEM)
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		certsHits++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		}, nil
	})}

	verifier, err := NewAccessVerifier(HandlerConfig{
		AccessAUD:       "aud-1",
		AccessIssuerURL: "https://team.cloudflareaccess.com",
		AccessJWKSURL:   "https://team.cloudflareaccess.com/cdn-cgi/access/certs",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("NewAccessVerifier() error = %v", err)
	}
	verifier.client = client

	token := signAccessToken(t, privateKey, kid, map[string]any{
		"aud":   []string{"aud-1"},
		"email": "user@example.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"nbf":   time.Now().Add(-1 * time.Minute).Unix(),
		"iss":   "https://team.cloudflareaccess.com",
		"type":  "app",
		"sub":   "sub-1",
	})

	claims, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", claims.Email)
	}
	if claims.Subject != "sub-1" {
		t.Fatalf("Subject = %q, want sub-1", claims.Subject)
	}

	if _, err := verifier.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify() second call error = %v", err)
	}
	if certsHits != 1 {
		t.Fatalf("certs endpoint hits = %d, want 1", certsHits)
	}
}

func TestAccessVerifierRejectsAudienceMismatch(t *testing.T) {
	privateKey, certPEM := generateTestAccessCertificate(t)
	kid := "kid-2"

	verifier, err := NewAccessVerifier(HandlerConfig{
		AccessAUD:       "expected-aud",
		AccessIssuerURL: "https://team.cloudflareaccess.com",
		AccessJWKSURL:   "https://team.cloudflareaccess.com/cdn-cgi/access/certs",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("NewAccessVerifier() error = %v", err)
	}
	verifier.client = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(marshalAccessCertsResponse(t, kid, certPEM))),
		}, nil
	})}

	token := signAccessToken(t, privateKey, kid, map[string]any{
		"aud":   []string{"wrong-aud"},
		"email": "user@example.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"nbf":   time.Now().Add(-1 * time.Minute).Unix(),
		"iss":   "https://team.cloudflareaccess.com",
		"type":  "app",
		"sub":   "sub-1",
	})

	if _, err := verifier.Verify(context.Background(), token); err == nil {
		t.Fatal("Verify() expected audience mismatch error")
	}
}

func TestAccessVerifierRejectsDisallowedEmailDomain(t *testing.T) {
	privateKey, certPEM := generateTestAccessCertificate(t)
	kid := "kid-3"

	verifier, err := NewAccessVerifier(HandlerConfig{
		AccessAUD:                "aud-1",
		AccessIssuerURL:          "https://team.cloudflareaccess.com",
		AccessJWKSURL:            "https://team.cloudflareaccess.com/cdn-cgi/access/certs",
		AccessAllowedEmailDomain: "example.com",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("NewAccessVerifier() error = %v", err)
	}
	verifier.client = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(marshalAccessCertsResponse(t, kid, certPEM))),
		}, nil
	})}

	token := signAccessToken(t, privateKey, kid, map[string]any{
		"aud":   []string{"aud-1"},
		"email": "user@other.example",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"nbf":   time.Now().Add(-1 * time.Minute).Unix(),
		"iss":   "https://team.cloudflareaccess.com",
		"type":  "app",
		"sub":   "sub-1",
	})

	if _, err := verifier.Verify(context.Background(), token); err == nil {
		t.Fatal("Verify() expected email domain error")
	}
}

func generateTestAccessCertificate(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-access-cert",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() error = %v", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
	return privateKey, string(pemBytes)
}

func signAccessToken(t *testing.T, privateKey *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()

	headerBytes, err := json.Marshal(map[string]string{
		"alg": "RS256",
		"kid": kid,
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerBytes) + "." + base64.RawURLEncoding.EncodeToString(payloadBytes)
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15(): %v", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func marshalAccessCertsResponse(t *testing.T, kid, certPEM string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"public_certs": []map[string]string{
			{"kid": kid, "cert": certPEM},
		},
	})
	if err != nil {
		t.Fatalf("marshal cert response: %v", err)
	}
	return string(body)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
