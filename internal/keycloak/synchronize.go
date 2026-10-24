package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// SynchronizeConfiguration updates only managed client settings, preserving the
// realm, users, sessions, client IDs and unrelated client attributes.
func (c *Client) SynchronizeConfiguration(ctx context.Context, username, password string) error {
	origin, err := url.Parse(c.config.ApplicationURL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("application URL must be an HTTP(S) origin")
	}
	if username == "" || password == "" {
		return errors.New("Keycloak administrator credentials are required for configuration synchronization")
	}
	var token tokenResponse
	if err = c.form(ctx, c.config.InternalURL+"/realms/master/protocol/openid-connect/token", url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {username}, "password": {password}}, &token); err != nil {
		// Do not include an identity server response that could echo credentials.
		return errors.New("cannot authenticate Keycloak configuration administrator; check its username and password")
	}
	if token.AccessToken == "" {
		return ErrUnauthorized
	}
	for _, client := range []struct {
		id, secret  string
		application bool
	}{{c.config.ClientID, c.config.ClientSecret, true}, {c.config.ManagementClientID, c.config.ManagementClientSecret, false}} {
		var representations []map[string]any
		endpoint := c.config.InternalURL + "/admin/realms/" + url.PathEscape(c.config.Realm) + "/clients"
		if err = c.configurationJSON(ctx, token.AccessToken, http.MethodGet, endpoint+"?clientId="+url.QueryEscape(client.id), nil, &representations); err != nil {
			return err
		}
		if len(representations) != 1 {
			return fmt.Errorf("expected one Keycloak client %q", client.id)
		}
		representation := representations[0]
		identifier, _ := representation["id"].(string)
		if identifier == "" {
			return errors.New("Keycloak client identifier is missing")
		}
		// Fetch the complete representation so PUT cannot discard unrelated settings.
		endpoint += "/" + url.PathEscape(identifier)
		if err = c.configurationJSON(ctx, token.AccessToken, http.MethodGet, endpoint, nil, &representation); err != nil {
			return err
		}
		if client.secret != "" {
			representation["secret"] = client.secret
		}
		if client.application {
			representation["rootUrl"] = c.config.ApplicationURL
			representation["baseUrl"] = c.config.ApplicationURL
			representation["redirectUris"] = []string{c.callbackURL()}
			representation["webOrigins"] = []string{c.config.ApplicationURL}
			attributes, _ := representation["attributes"].(map[string]any)
			if attributes == nil {
				attributes = map[string]any{}
			}
			attributes["post.logout.redirect.uris"] = c.config.ApplicationURL
			representation["attributes"] = attributes
		}
		if err = c.configurationJSON(ctx, token.AccessToken, http.MethodPut, endpoint, representation, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) configurationJSON(ctx context.Context, token, method, endpoint string, payload, target any) error {
	var body io.Reader
	if payload != nil {
		value, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(value)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Keycloak configuration request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Keycloak configuration %s failed: %s", method, response.Status)
	}
	if target != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
	}
	return nil
}
