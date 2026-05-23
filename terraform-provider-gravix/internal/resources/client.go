package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client wraps HTTP calls to the Gravix gateway API.
type Client struct {
	APIURL   string
	JWTToken string
	http     *http.Client
}

func NewClient(apiURL, jwtToken string) *Client {
	return &Client{
		APIURL:   apiURL,
		JWTToken: jwtToken,
		http:     &http.Client{},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.APIURL+path, r)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.JWTToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func clientFromMeta(meta any) *Client {
	type iface interface {
		APIURL() string
		JWTToken() string
	}
	// meta is *provider.Client; use map access to avoid import cycle.
	m := meta.(map[string]string)
	return NewClient(m["api_url"], m["jwt_token"])
}
