package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrConflict = errors.New("keycloak conflict")
var ErrUnauthorized = errors.New("keycloak unauthorized")

type Config struct {
	InternalURL            string
	PublicURL              string
	ApplicationURL         string
	Realm                  string
	ClientID               string
	ClientSecret           string
	ManagementClientID     string
	ManagementClientSecret string
}

type Client struct {
	config Config
	http   *http.Client
}

type Identity struct {
	Subject  string `json:"sub"`
	Username string `json:"preferred_username"`
	Email    string `json:"email"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
}

func New(config Config) *Client {
	config.InternalURL = strings.TrimRight(config.InternalURL, "/")
	config.PublicURL = strings.TrimRight(config.PublicURL, "/")
	config.ApplicationURL = strings.TrimRight(config.ApplicationURL, "/")
	return &Client{config: config, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) ProxyTarget() string {
	target, err := url.Parse(c.config.InternalURL)
	if err != nil {
		return c.config.InternalURL
	}
	target.Path = ""
	target.RawPath = ""
	target.RawQuery = ""
	return target.String()
}

func (c *Client) AuthorizationURL(state, challenge string) string {
	query := url.Values{
		"client_id":             {c.config.ClientID},
		"redirect_uri":          {c.callbackURL()},
		"response_type":         {"code"},
		"scope":                 {"openid profile email"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return c.realmURL(c.config.PublicURL, "/protocol/openid-connect/auth") + "?" + query.Encode()
}

func (c *Client) LogoutURL() string {
	query := url.Values{"client_id": {c.config.ClientID}, "post_logout_redirect_uri": {c.config.ApplicationURL}}
	return c.realmURL(c.config.PublicURL, "/protocol/openid-connect/logout") + "?" + query.Encode()
}

func (c *Client) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.config.ClientID},
		"client_secret": {c.config.ClientSecret},
		"redirect_uri":  {c.callbackURL()},
		"code":          {code},
		"code_verifier": {verifier},
	}
	var token tokenResponse
	if err := c.form(ctx, c.realmURL(c.config.InternalURL, "/protocol/openid-connect/token"), values, &token); err != nil {
		return Identity{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.realmURL(c.config.InternalURL, "/protocol/openid-connect/userinfo"), nil)
	if err != nil {
		return Identity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	response, err := c.http.Do(request)
	if err != nil {
		return Identity{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Identity{}, responseError(response)
	}
	var identity Identity
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity); err != nil {
		return Identity{}, err
	}
	if identity.Subject == "" || identity.Username == "" {
		return Identity{}, errors.New("keycloak returned an incomplete identity")
	}
	return identity, nil
}

func (c *Client) CreateUser(ctx context.Context, username, email, password string) (string, error) {
	return c.createUser(ctx, username, email, password, false)
}

// CreateUserWithTemporaryPassword requires a password change during first login.
func (c *Client) CreateUserWithTemporaryPassword(ctx context.Context, username, email, password string) (string, error) {
	return c.createUser(ctx, username, email, password, true)
}

func (c *Client) createUser(ctx context.Context, username, email, password string, temporary bool) (string, error) {
	token, err := c.adminToken(ctx)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"username":      username,
		"firstName":     username,
		"lastName":      username,
		"email":         email,
		"enabled":       true,
		"emailVerified": email != "",
		"credentials":   []map[string]any{{"type": "password", "value": password, "temporary": temporary}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	endpoint := c.config.InternalURL + "/admin/realms/" + url.PathEscape(c.config.Realm) + "/users"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return "", ErrConflict
	}
	if response.StatusCode != http.StatusCreated {
		return "", responseError(response)
	}
	location := response.Header.Get("Location")
	if index := strings.LastIndex(location, "/"); index >= 0 && index+1 < len(location) {
		return location[index+1:], nil
	}
	return c.findUserID(ctx, token, username)
}

func (c *Client) DeleteUser(ctx context.Context, externalID string) error {
	token, err := c.adminToken(ctx)
	if err != nil {
		return err
	}
	endpoint := c.config.InternalURL + "/admin/realms/" + url.PathEscape(c.config.Realm) + "/users/" + url.PathEscape(externalID)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return responseError(response)
	}
	return nil
}

func (c *Client) Ready(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.realmURL(c.config.InternalURL, "/.well-known/openid-configuration"), nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	return nil
}

func (c *Client) adminToken(ctx context.Context) (string, error) {
	values := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.config.ManagementClientID},
		"client_secret": {c.config.ManagementClientSecret},
	}
	var token tokenResponse
	if err := c.form(ctx, c.realmURL(c.config.InternalURL, "/protocol/openid-connect/token"), values, &token); err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", ErrUnauthorized
	}
	return token.AccessToken, nil
}

func (c *Client) findUserID(ctx context.Context, token, username string) (string, error) {
	query := url.Values{"username": {username}, "exact": {"true"}}
	endpoint := c.config.InternalURL + "/admin/realms/" + url.PathEscape(c.config.Realm) + "/users?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", responseError(response)
	}
	var users []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&users); err != nil {
		return "", err
	}
	if len(users) != 1 || users[0].ID == "" {
		return "", errors.New("keycloak user was created without an identifier")
	}
	return users[0].ID, nil
}

func (c *Client) form(ctx context.Context, endpoint string, values url.Values, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func (c *Client) callbackURL() string {
	return c.config.ApplicationURL + "/api/v1/auth/callback"
}

func (c *Client) realmURL(base, suffix string) string {
	return base + "/realms/" + url.PathEscape(c.config.Realm) + suffix
}

func responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("keycloak returned %s: %s", response.Status, strings.TrimSpace(string(body)))
}

// UpdateUserEmail leaves roles, required actions and other identity fields intact.
func (c *Client) UpdateUserEmail(ctx context.Context, externalID, email string) error {
	return c.userAdminRequest(ctx, http.MethodPut, externalID, "", map[string]any{"email": email, "emailVerified": false})
}

func (c *Client) ResetTemporaryPassword(ctx context.Context, externalID, password string) error {
	if err := c.userAdminRequest(ctx, http.MethodPut, externalID, "/reset-password", map[string]any{"type": "password", "value": password, "temporary": true}); err != nil {
		return err
	}
	return c.userAdminRequest(ctx, http.MethodPost, externalID, "/logout", nil)
}

func (c *Client) userAdminRequest(ctx context.Context, method, externalID, suffix string, value any) error {
	if externalID == "" {
		return errors.New("identity is unavailable")
	}
	token, err := c.adminToken(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if value != nil {
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		body = strings.NewReader(string(raw))
	}
	endpoint := c.config.InternalURL + "/admin/realms/" + url.PathEscape(c.config.Realm) + "/users/" + url.PathEscape(externalID) + suffix
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if response.StatusCode != http.StatusNoContent {
		return responseError(response)
	}
	return nil
}
