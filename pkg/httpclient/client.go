package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go-fiber/pkg/logs"
)

const defaultTimeout = 10 * time.Second

// HttpRequest is the low-level interface — use Do when you need full control over request/response.
type HttpRequest interface {
	Do(logKey string, req *http.Request) (*http.Response, error)
	DoNonLogging(logKey string, req *http.Request) (*http.Response, error)
}

// Client is the high-level interface with JSON helpers built on top of HttpRequest.
type Client interface {
	HttpRequest
	BaseURL() string
	DoJSON(logKey string, req *http.Request, result interface{}) error
	Get(ctx context.Context, logKey string, path string, result interface{}) error
	Post(ctx context.Context, logKey string, path string, body interface{}, result interface{}) error
}

type CustomHTTPClient struct {
	baseURL string
	Client  *http.Client
	headers map[string]string
}

type Option func(*CustomHTTPClient)

func WithTimeout(d time.Duration) Option {
	return func(c *CustomHTTPClient) {
		c.Client.Timeout = d
	}
}

func WithHeader(key, value string) Option {
	return func(c *CustomHTTPClient) {
		c.headers[key] = value
	}
}

// WithInsecureSkipVerify disables TLS certificate verification for this client only.
// Use only for known internal endpoints presenting a cert this host doesn't trust
// (e.g. a private/self-signed CA) — never for public-facing services.
func WithInsecureSkipVerify() Option {
	return func(c *CustomHTTPClient) {
		c.Client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // scoped opt-in, see doc comment
		}
	}
}

func NewClient(baseURL string, opts ...Option) Client {
	if baseURL == "" {
		logs.Info("httpclient: baseURL is empty — requests will fail. Check the configured base URL")
	}
	c := &CustomHTTPClient{
		baseURL: baseURL,
		Client:  &http.Client{Timeout: defaultTimeout},
		headers: make(map[string]string),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *CustomHTTPClient) BaseURL() string {
	return c.baseURL
}

// DoJSON calls Do then decodes the JSON response body into result.
func (c *CustomHTTPClient) DoJSON(logKey string, req *http.Request, result interface{}) error {
	resp, err := c.Do(logKey, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *CustomHTTPClient) Do(logKey string, req *http.Request) (*http.Response, error) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	logs.Info(fmt.Sprintf("key:%v Method %v : %v", logKey, req.Method, req.URL))

	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			logs.Info(fmt.Sprintf("key:%v Error reading request body: %v", logKey, err))
		} else {
			logs.Info(fmt.Sprintf("key:%v Request: %s", logKey, body))
			req.Body = io.NopCloser(bytes.NewBuffer(body))
		}
	}

	req.Close = true
	resp, err := c.Client.Do(req)

	if err != nil {
		logs.Info(fmt.Sprintf("key:%v Error sending request: %v", logKey, err))
		return nil, err
	}

	if req.Method != http.MethodGet && req.Method != http.MethodOptions {
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			logs.Info(fmt.Sprintf("key:%v Error reading response body: %v", logKey, err))
		} else {
			logs.Info(fmt.Sprintf("key:%v Response: %s", logKey, responseBody))
			resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
		}
		logs.Info(fmt.Sprintf("REQUEST_PATH# '%v', RESPONSE_STATUS_CODE# '%v', RESPONSE# '%v'",
			resp.Request.URL.RawPath, resp.StatusCode, string(responseBody)))
	}

	return resp, nil
}

func (c *CustomHTTPClient) DoNonLogging(logKey string, req *http.Request) (*http.Response, error) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			logs.Info(fmt.Sprintf("key:%v Error reading request body: %v", logKey, err))
		} else {
			req.Body = io.NopCloser(bytes.NewBuffer(body))
		}
	}

	req.Close = true
	resp, err := c.Client.Do(req)

	if err != nil {
		logs.Info(fmt.Sprintf("key:%v Error sending request: %v", logKey, err))
		return nil, err
	}

	if req.Method != http.MethodGet && req.Method != http.MethodOptions {
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			logs.Info(fmt.Sprintf("key:%v Error reading response body: %v", logKey, err))
		} else {
			resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
		}
	}

	return resp, nil
}

func (c *CustomHTTPClient) Get(ctx context.Context, logKey string, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(logKey, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *CustomHTTPClient) Post(ctx context.Context, logKey string, path string, body interface{}, result interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(logKey, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}
