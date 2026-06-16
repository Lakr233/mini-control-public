package rest

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const accessJWTHeader = "Cf-Access-Jwt-Assertion"

type AccessVerifier struct {
	audience           string
	issuerURL          string
	certsURL           string
	allowedEmailDomain string
	client             *http.Client
	logger             *slog.Logger

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

type accessJWTHeaderFields struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type AccessClaims struct {
	Audience  []string `json:"aud"`
	Email     string   `json:"email"`
	Expires   int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf"`
	Issuer    string   `json:"iss"`
	Type      string   `json:"type"`
	Subject   string   `json:"sub"`
}

type accessAudience []string

func (a *accessAudience) UnmarshalJSON(data []byte) error {
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*a = many
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = []string{single}
		return nil
	}

	return errors.New("invalid aud claim")
}

func NewAccessVerifier(cfg HandlerConfig, logger *slog.Logger) (*AccessVerifier, error) {
	if cfg.AccessAUD == "" {
		return nil, nil
	}

	issuerURL := strings.TrimRight(cfg.AccessIssuerURL, "/")
	certsURL := strings.TrimSpace(cfg.AccessJWKSURL)

	if issuerURL == "" {
		if cfg.AccessTeamDomain == "" {
			return nil, errors.New("access_team_domain is required when access_aud is configured")
		}
		raw := strings.TrimSpace(cfg.AccessTeamDomain)
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("invalid access_team_domain: %q", cfg.AccessTeamDomain)
		}
		parsed.Path = ""
		parsed.RawPath = ""
		parsed.RawQuery = ""
		parsed.Fragment = ""
		issuerURL = strings.TrimRight(parsed.String(), "/")
	}

	if certsURL == "" {
		certsURL = issuerURL + "/cdn-cgi/access/certs"
	}

	return &AccessVerifier{
		audience:           cfg.AccessAUD,
		issuerURL:          issuerURL,
		certsURL:           certsURL,
		allowedEmailDomain: normalizeAccessEmailDomain(cfg.AccessAllowedEmailDomain),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
		keys:   map[string]*rsa.PublicKey{},
	}, nil
}

func (v *AccessVerifier) Verify(ctx context.Context, token string) (*AccessClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var header accessJWTHeaderFields
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported jwt alg %q", header.Alg)
	}
	if header.Kid == "" {
		return nil, errors.New("missing key id")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var parsed struct {
		Audience accessAudience `json:"aud"`
		Email    string         `json:"email"`
		Exp      int64          `json:"exp"`
		Iat      int64          `json:"iat"`
		Nbf      int64          `json:"nbf"`
		Iss      string         `json:"iss"`
		Type     string         `json:"type"`
		Sub      string         `json:"sub"`
	}
	if err := json.Unmarshal(payloadBytes, &parsed); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}

	claims := &AccessClaims{
		Audience:  []string(parsed.Audience),
		Email:     parsed.Email,
		Expires:   parsed.Exp,
		IssuedAt:  parsed.Iat,
		NotBefore: parsed.Nbf,
		Issuer:    parsed.Iss,
		Type:      parsed.Type,
		Subject:   parsed.Sub,
	}
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}

	publicKey, err := v.lookupKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, sum[:], signature); err != nil {
		return nil, errors.New("invalid token signature")
	}

	return claims, nil
}

func (v *AccessVerifier) validateClaims(claims *AccessClaims) error {
	now := time.Now().Unix()
	if claims.Issuer != v.issuerURL {
		return fmt.Errorf("unexpected issuer %q", claims.Issuer)
	}
	if claims.Type != "app" {
		return fmt.Errorf("unexpected token type %q", claims.Type)
	}
	if !containsString(claims.Audience, v.audience) {
		return errors.New("audience mismatch")
	}
	if claims.Expires == 0 || now >= claims.Expires {
		return errors.New("token expired")
	}
	if claims.NotBefore != 0 && now+60 < claims.NotBefore {
		return errors.New("token not valid yet")
	}
	if claims.Subject == "" {
		return errors.New("missing subject")
	}
	if claims.Email == "" {
		return errors.New("missing email")
	}
	if v.allowedEmailDomain != "" && !accessEmailMatchesDomain(claims.Email, v.allowedEmailDomain) {
		return errors.New("email domain not allowed")
	}
	return nil
}

func normalizeAccessEmailDomain(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	domain = strings.TrimPrefix(domain, "@")
	return domain
}

func accessEmailMatchesDomain(email, domain string) bool {
	email = strings.TrimSpace(strings.ToLower(email))
	domain = normalizeAccessEmailDomain(domain)
	if email == "" || domain == "" {
		return false
	}
	return strings.HasSuffix(email, "@"+domain)
}

func (v *AccessVerifier) lookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key := v.keys[kid]
	stale := time.Since(v.fetchedAt) > 5*time.Minute
	v.mu.RUnlock()
	if key != nil && !stale {
		return key, nil
	}

	if err := v.refreshKeys(ctx); err != nil {
		return nil, err
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	key = v.keys[kid]
	if key == nil {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	return key, nil
}

func (v *AccessVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch access certs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch access certs: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read access certs: %w", err)
	}

	keys, err := parseAccessCerts(body)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

func parseAccessCerts(body []byte) (map[string]*rsa.PublicKey, error) {
	type certResponse struct {
		PublicCerts []struct {
			Kid  string `json:"kid"`
			Cert string `json:"cert"`
		} `json:"public_certs"`
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}

	var payload certResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse access certs: %w", err)
	}

	result := map[string]*rsa.PublicKey{}
	for _, cert := range payload.PublicCerts {
		publicKey, err := parsePEMPublicKey(cert.Cert)
		if err != nil {
			return nil, err
		}
		result[cert.Kid] = publicKey
	}
	for _, key := range payload.Keys {
		if key.Kty != "RSA" || key.Kid == "" {
			continue
		}
		publicKey, err := parseJWKPublicKey(key.N, key.E)
		if err != nil {
			return nil, err
		}
		result[key.Kid] = publicKey
	}
	if len(result) == 0 {
		return nil, errors.New("access cert response contained no usable keys")
	}
	return result, nil
}

func parsePEMPublicKey(raw string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("invalid PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("certificate did not contain RSA public key")
	}
	return publicKey, nil
}

func parseJWKPublicKey(nRaw, eRaw string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nRaw)
	if err != nil {
		return nil, fmt.Errorf("decode rsa modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eRaw)
	if err != nil {
		return nil, fmt.Errorf("decode rsa exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, errors.New("invalid rsa exponent")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
