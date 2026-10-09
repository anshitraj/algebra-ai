// Package identity holds the outbound adapters for human sign-in: OAuth 2.0
// authorization-code clients for Google and GitHub, and the transactional
// mailer used for password reset. Nothing here decides who gets an
// account — that's app.AccountService; these only talk to the providers.
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/project-algebra/algebra/internal/domain/account"
)

// Provider is one OAuth sign-in provider.
type Provider interface {
	Name() string
	// AuthCodeURL is where to send the browser; state and the PKCE verifier
	// are generated and remembered by the caller.
	AuthCodeURL(state, verifier, redirectURL string) string
	// Exchange trades the callback's code for a verified profile.
	Exchange(ctx context.Context, code, verifier, redirectURL string) (account.OAuthProfile, error)
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

func oauthConfig(id, secret string, ep oauth2.Endpoint, scopes []string, redirectURL string) *oauth2.Config {
	return &oauth2.Config{ClientID: id, ClientSecret: secret, Endpoint: ep, Scopes: scopes, RedirectURL: redirectURL}
}

func getJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: %s returned %d", url, resp.StatusCode)
	}
	return json.Unmarshal(body, v)
}

// --- Google (OpenID Connect userinfo) ---

type google struct{ id, secret string }

// NewGoogle returns a Google sign-in provider. Create the OAuth client at
// console.cloud.google.com → APIs & Services → Credentials (type "Web
// application"), with <PUBLIC_WEB_URL>/api/v1/auth/oauth/google/callback
// as an authorized redirect URI.
func NewGoogle(clientID, clientSecret string) Provider {
	return &google{id: clientID, secret: clientSecret}
}

func (g *google) Name() string { return "google" }

func (g *google) cfg(redirectURL string) *oauth2.Config {
	return oauthConfig(g.id, g.secret, endpoints.Google, []string{"openid", "email", "profile"}, redirectURL)
}

func (g *google) AuthCodeURL(state, verifier, redirectURL string) string {
	return g.cfg(redirectURL).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"))
}

func (g *google) Exchange(ctx context.Context, code, verifier, redirectURL string) (account.OAuthProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	cfg := g.cfg(redirectURL)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return account.OAuthProfile{}, fmt.Errorf("identity: google code exchange failed: %w", err)
	}
	var info struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := getJSON(ctx, cfg.Client(ctx, tok), "https://openidconnect.googleapis.com/v1/userinfo", nil, &info); err != nil {
		return account.OAuthProfile{}, fmt.Errorf("identity: google userinfo: %w", err)
	}
	return account.OAuthProfile{
		Provider: "google", ProviderUserID: info.Sub, Email: info.Email,
		EmailVerified: info.EmailVerified, Name: info.Name, AvatarURL: info.Picture,
	}, nil
}

// --- GitHub ---

type github struct{ id, secret string }

// NewGitHub returns a GitHub sign-in provider. Create an OAuth App at
// github.com/settings/developers with
// <PUBLIC_WEB_URL>/api/v1/auth/oauth/github/callback as the callback URL.
func NewGitHub(clientID, clientSecret string) Provider {
	return &github{id: clientID, secret: clientSecret}
}

func (g *github) Name() string { return "github" }

func (g *github) cfg(redirectURL string) *oauth2.Config {
	return oauthConfig(g.id, g.secret, endpoints.GitHub, []string{"read:user", "user:email"}, redirectURL)
}

func (g *github) AuthCodeURL(state, verifier, redirectURL string) string {
	return g.cfg(redirectURL).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

func (g *github) Exchange(ctx context.Context, code, verifier, redirectURL string) (account.OAuthProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	cfg := g.cfg(redirectURL)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return account.OAuthProfile{}, fmt.Errorf("identity: github code exchange failed: %w", err)
	}
	client := cfg.Client(ctx, tok)
	headers := map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}

	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user", headers, &user); err != nil {
		return account.OAuthProfile{}, fmt.Errorf("identity: github user: %w", err)
	}
	if user.ID == 0 {
		return account.OAuthProfile{}, errors.New("identity: github returned no user id")
	}
	// The profile's public email may be empty or unverified; /user/emails
	// is the authoritative, verification-aware list.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user/emails", headers, &emails); err != nil {
		return account.OAuthProfile{}, fmt.Errorf("identity: github emails: %w", err)
	}
	var email string
	verified := false
	for _, e := range emails {
		if e.Primary && e.Verified {
			email, verified = e.Email, true
			break
		}
	}
	if email == "" {
		for _, e := range emails {
			if e.Verified && !strings.HasSuffix(e.Email, "@users.noreply.github.com") {
				email, verified = e.Email, true
				break
			}
		}
	}
	name := user.Name
	if name == "" {
		name = user.Login
	}
	return account.OAuthProfile{
		Provider: "github", ProviderUserID: strconv.FormatInt(user.ID, 10), Email: email,
		EmailVerified: verified, Name: name, AvatarURL: user.AvatarURL,
	}, nil
}
