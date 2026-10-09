package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sheetsScope     = "https://www.googleapis.com/auth/spreadsheets"
	defaultTokenURI = "https://oauth2.googleapis.com/token"
)

// serviceAccountKey is the subset of a Google service account JSON key we need.
type serviceAccountKey struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`

	privateKey *rsa.PrivateKey
}

// sheetRecorder appends one row per completed burst to a spreadsheet.
// It stays inert (Append returns why) when the credentials are not configured,
// so the engine keeps running without them.
type sheetRecorder struct {
	sheetID string
	tab     string
	key     *serviceAccountKey
	client  *http.Client
	err     error

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newSheetRecorder() *sheetRecorder {
	r := &sheetRecorder{
		client: &http.Client{Timeout: 15 * time.Second},
		tab:    os.Getenv("GOOGLE_SHEET_TAB"),
	}
	if r.tab == "" {
		r.tab = "Sheet1"
	}

	sheetID := os.Getenv("GOOGLE_SHEET_ID")
	raw := os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")

	switch {
	case sheetID == "":
		r.err = errors.New("GOOGLE_SHEET_ID is not set")
	case raw == "":
		r.err = errors.New("GOOGLE_SERVICE_ACCOUNT_JSON is not set")
	default:
		key, err := parseServiceAccount(raw)
		if err != nil {
			r.err = err
		} else {
			r.sheetID, r.key = sheetID, key
		}
	}

	if r.err != nil {
		log.Printf("[SHEETS] burst history disabled: %v", r.err)
	} else {
		log.Printf("[SHEETS] burst history enabled for spreadsheet %s (tab %q)", r.sheetID, r.tab)
	}
	return r
}

func parseServiceAccount(raw string) (*serviceAccountKey, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		decoded, err := base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(trimmed)
		}
		if err != nil {
			return nil, fmt.Errorf("service account key is neither JSON nor base64: %w", err)
		}
		trimmed = string(decoded)
	}

	var key serviceAccountKey
	if err := json.Unmarshal([]byte(trimmed), &key); err != nil {
		return nil, fmt.Errorf("parsing service account key: %w", err)
	}
	if key.ClientEmail == "" {
		return nil, errors.New("service account key has no client_email")
	}

	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return nil, errors.New("service account key has no PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing service account private key: %w", err)
	}
	priv, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("service account private key is not RSA")
	}
	key.privateKey = priv
	if key.TokenURI == "" {
		key.TokenURI = defaultTokenURI
	}
	return &key, nil
}

// Append writes one burst result as a row: timestamp, count, duration, throughput, total.
func (r *sheetRecorder) Append(res burstResult) error {
	if r.err != nil {
		return r.err
	}

	token, err := r.accessToken()
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf(
		"https://sheets.googleapis.com/v4/spreadsheets/%s/values/%s:append?valueInputOption=USER_ENTERED&insertDataOption=INSERT_ROWS",
		url.PathEscape(r.sheetID), url.PathEscape(r.tab+"!A:E"),
	)
	row := []string{
		res.StartedAt.Format(time.RFC3339),
		strconv.FormatUint(res.Count, 10),
		strconv.FormatInt(res.DurationMS, 10),
		strconv.FormatFloat(res.ThroughputTxS, 'f', 1, 64),
		strconv.FormatUint(res.TotalProcessed, 10),
	}
	body, err := json.Marshal(map[string]any{"values": [][]string{row}})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("sheets append failed: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// accessToken exchanges a signed service account assertion for an OAuth token,
// caching it until shortly before it expires.
func (r *sheetRecorder) accessToken() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.token != "" && now.Before(r.expiry.Add(-time.Minute)) {
		return r.token, nil
	}

	assertion, err := signAssertion(r.key, now)
	if err != nil {
		return "", err
	}

	resp, err := r.client.PostForm(r.key.TokenURI, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("token exchange failed: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", errors.New("token response carried no access_token")
	}

	r.token = payload.AccessToken
	r.expiry = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
	return r.token, nil
}

// signAssertion builds the RS256 JWT the token endpoint expects.
func signAssertion(key *serviceAccountKey, now time.Time) (string, error) {
	claims, err := json.Marshal(map[string]any{
		"iss":   key.ClientEmail,
		"scope": sheetsScope,
		"aud":   key.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}

	enc := base64.RawURLEncoding.EncodeToString
	signing := enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc(sig), nil
}
