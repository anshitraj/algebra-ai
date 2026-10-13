// Package gcpkms signs Solana transactions with a Cloud KMS key, so the payer's
// private key never enters this process. It uses application default
// credentials (a service account on Google Cloud, a credentials file elsewhere)
// and the Cloud KMS REST API, and returns a solana.Keypair whose signatures
// come from KMS.
//
// What this protects, and what it does not. A server that can ask KMS for
// signatures can still ask for the wrong ones if it is compromised; what it
// cannot do is take the key away and sign from somewhere else, later, at will.
// The limits that bound a compromised server are the Spend Pass program's
// (a vault pays out only what its owner allowed, and the owner can freeze it),
// KMS's own audit log, and the service account's permission to sign with this
// one key version and nothing else.
//
// Errors never contain a response body or a token.
package gcpkms

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

const endpoint = "https://cloudkms.googleapis.com/v1/"

// A CryptoKeyVersion, which names one exact key: a signer is never "whatever
// version is primary now", because a different key is a different payer.
var (
	versionName = regexp.MustCompile(`^projects/[A-Za-z0-9_-]+/locations/[A-Za-z0-9_-]+/keyRings/[A-Za-z0-9_-]+/cryptoKeys/[A-Za-z0-9_-]+/cryptoKeyVersions/[0-9]+$`)
)

// attempts bounds how often a call is tried; signing is in the path of a payment
// someone is waiting for. Ed25519 signatures are deterministic, so asking again
// is harmless.
const attempts = 3

// Client talks to Cloud KMS.
type Client struct {
	http *http.Client
	base string
	// backoff is the wait before attempt n (0-based after the first); a test sets it to zero.
	backoff func(n int) time.Duration
}

// New builds a Client from application default credentials.
func New(ctx context.Context) (*Client, error) {
	source, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, errors.New("kms: application default credentials unavailable")
	}
	return &Client{http: &http.Client{Transport: &oauth2.Transport{Source: source}, Timeout: 15 * time.Second}, base: endpoint, backoff: defaultBackoff}, nil
}

func defaultBackoff(n int) time.Duration { return time.Duration(200*(1<<n)) * time.Millisecond }

// request makes one KMS call, trying again on a network failure, 429 or 5xx.
func (c *Client) request(ctx context.Context, method, name string, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	wait := c.backoff
	if wait == nil {
		wait = defaultBackoff
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return errors.New("kms: request cancelled")
			case <-time.After(wait(i - 1)):
			}
		}
		retry, err := c.once(ctx, method, name, payload, out)
		if err == nil {
			return nil
		}
		last = err
		if !retry {
			return err
		}
	}
	return last
}

// once is one attempt; retry says whether another could help.
func (c *Client) once(ctx context.Context, method, name string, payload []byte, out any) (retry bool, err error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+name, body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return true, errors.New("kms: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		transient := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5
		return transient, fmt.Errorf("kms: operation refused (HTTP %d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return false, errors.New("kms: malformed response")
	}
	return false, nil
}

// Signer returns the solana.Keypair for one exact Ed25519 CryptoKeyVersion
// ("projects/…/cryptoKeyVersions/N"). Every signature is checked against the
// key's public half before it is used (solana.NewRemoteSigner).
func (c *Client) Signer(ctx context.Context, version string) (*solana.Keypair, error) {
	if !versionName.MatchString(version) {
		return nil, errors.New("kms: a signer needs one exact CryptoKeyVersion (projects/…/cryptoKeyVersions/N)")
	}
	var public struct {
		Pem       string `json:"pem"`
		Algorithm string `json:"algorithm"`
	}
	if err := c.request(ctx, http.MethodGet, version+"/publicKey", nil, &public); err != nil {
		return nil, err
	}
	if public.Algorithm != "EC_SIGN_ED25519" {
		return nil, errors.New("kms: the key must be a pure Ed25519 signing key (EC_SIGN_ED25519)")
	}
	block, _ := pem.Decode([]byte(public.Pem))
	if block == nil {
		return nil, errors.New("kms: malformed public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("kms: invalid public key")
	}
	ed, ok := key.(ed25519.PublicKey)
	if !ok || len(ed) != ed25519.PublicKeySize {
		return nil, errors.New("kms: the public key is not Ed25519")
	}
	var pk solana.PublicKey
	copy(pk[:], ed)
	return solana.NewRemoteSigner(pk, func(message []byte) ([64]byte, error) {
		// Sign has no context to pass: bound the call here.
		callCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var out struct {
			Signature string `json:"signature"`
			Name      string `json:"name"`
		}
		// The message goes to KMS as it is: Ed25519 signs the raw bytes.
		if err := c.request(callCtx, http.MethodPost, version+":asymmetricSign", map[string]string{"data": base64.StdEncoding.EncodeToString(message)}, &out); err != nil {
			return [64]byte{}, err
		}
		raw, err := base64.StdEncoding.DecodeString(out.Signature)
		if err != nil || len(raw) != ed25519.SignatureSize || out.Name != version {
			return [64]byte{}, errors.New("kms: invalid signing response")
		}
		var sig [64]byte
		copy(sig[:], raw)
		return sig, nil
	}), nil
}
