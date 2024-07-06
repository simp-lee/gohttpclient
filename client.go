package gohttpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	"golang.org/x/time/rate"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// ClientError represents all possible errors returned by the client
type ClientError struct {
	Op   string // operation that failed
	Err  error  // underlying error
	Code int    // HTTP status code, if applicable
}

func (e *ClientError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s failed: %v (HTTP %d)", e.Op, e.Err, e.Code)
	}
	return fmt.Sprintf("%s failed: %v", e.Op, e.Err)
}

// Client represents a custom HTTP client with various optimizations
type Client struct {
	http.Client
	headers              map[string]string
	requestInterceptors  []RequestInterceptor
	responseInterceptors []ResponseInterceptor
	metrics              *Metrics
	baseContext          context.Context
	cancelFunc           context.CancelFunc
	limiter              *rate.Limiter
	logger               Logger
	retries              int
	backOff              backoff.BackOff
}

// Metrics holds metrics for the client
type Metrics struct {
	RequestCount int64
	ErrorCount   int64
	TotalLatency int64 // in nanoseconds
}

// RequestInterceptor is a function that can modify requests before they are sent
type RequestInterceptor func(*http.Request) error

// ResponseInterceptor is a function that can modify responses after they are received
type ResponseInterceptor func(*http.Response) error

// Logger is a simple interface for logging messages
type Logger interface {
	Info(msg string, keyVals ...interface{})
	Error(msg string, keyVals ...interface{})
}

// ClientOption is a function type for client configuration
type ClientOption func(client *Client)

// WithTimeout sets the overall timeout for requests
// This includes connection time, any redirects, and reading the response body
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.Timeout = timeout
	}
}

// WithRetries sets the number of retries for failed requests
// Retries are performed using exponential backoff
func WithRetries(retries int) ClientOption {
	return func(c *Client) {
		c.retries = retries
	}
}

// WithBackoff sets a custom backoff strategy for retries
// Use this if the default exponential backoff doesn't suit your needs
func WithBackoff(b backoff.BackOff) ClientOption {
	return func(c *Client) {
		c.backOff = b
	}
}

// WithRateLimit sets rate limiting for requests
// rps is the maximum number of requests per second
// burst is the maximum number of requests that can be sent at once
func WithRateLimit(rps float64, burst int) ClientOption {
	return func(c *Client) {
		c.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
}

// WithLogger sets a custom logger
func WithLogger(logger Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithMaxIdleConns sets the maximum number of idle connections
// Increasing this can improve performance for concurrent requests to many hosts
func WithMaxIdleConns(n int) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			transport.MaxIdleConns = n
		}
	}
}

// WithMaxConnsPerHost sets the maximum number of connections per host
// This can prevent a single host from using too many resources
func WithMaxConnsPerHost(n int) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			transport.MaxConnsPerHost = n
		}
	}
}

// WithIdleConnTimeout sets the maximum amount of time an idle connection will remain idle before closing itself
// Lower values can help manage resource usage, but may increase latency for new requests
func WithIdleConnTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			transport.IdleConnTimeout = d
		}
	}
}

// WithTLSHandshakeTimeout sets the maximum amount of time waiting for a TLS handshake
// Increase this if you're dealing with slow or unreliable networks
func WithTLSHandshakeTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			transport.TLSHandshakeTimeout = d
		}
	}
}

// WithDisableKeepAlives disables HTTP keep-alives
// This can be useful if you're dealing with servers that don't support keep-alives well
func WithDisableKeepAlives(disable bool) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			transport.DisableKeepAlives = disable
		}
	}
}

// WithDisableCompression disables automatic decompression of the response body
// This can be useful if you want to handle decompression yourself
func WithDisableCompression(disable bool) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			transport.DisableCompression = disable
		}
	}
}

// WithProxy sets a proxy for the client
// If proxyURL set nil, no proxy will be used, even if the environment variables are set
func WithProxy(proxyURL *url.URL) ClientOption {
	return func(c *Client) {
		if transport, ok := c.Transport.(*http.Transport); ok {
			if proxyURL != nil {
				transport.Proxy = http.ProxyURL(proxyURL)
			} else {
				transport.Proxy = nil
			}
		}
	}
}

// NewClient creates a new Client with the given options
func NewClient(options ...ClientOption) *Client {
	baseContext, cancelFunc := context.WithCancel(context.Background())

	// Default transport settings
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // Maximum time for connection establishment
			KeepAlive: 30 * time.Second, // Interval between keep-alive probes
		}).DialContext,
		ForceAttemptHTTP2:     true,             // Enable HTTP/2 support
		MaxIdleConns:          100,              // Maximum number of idle connections across all hosts
		MaxConnsPerHost:       10,               // Maximum number of connections per host
		IdleConnTimeout:       90 * time.Second, // How long to keep idle connections alive
		TLSHandshakeTimeout:   10 * time.Second, // Maximum time waiting for a TLS handshake
		ExpectContinueTimeout: 1 * time.Second,  // Maximum time waiting for a 100-continue response
		DisableKeepAlives:     false,            // Enable HTTP keep-alives
		DisableCompression:    false,            // Enable automatic response compression
		// Proxy is not set by default
	}

	// Create client with default settings
	c := &Client{
		Client: http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		headers:     make(map[string]string),
		metrics:     &Metrics{},
		baseContext: baseContext,
		cancelFunc:  cancelFunc,
		logger:      &defaultLogger{},
		retries:     3,
		backOff:     backoff.NewExponentialBackOff(),
	}

	// Apply custom options
	for _, option := range options {
		option(c)
	}

	return c
}

// SetHeader sets a header for all requests
func (c *Client) SetHeader(key, value string) {
	c.headers[key] = value
}

// AddRequestInterceptor adds a request interceptor
func (c *Client) AddRequestInterceptor(interceptor RequestInterceptor) {
	c.requestInterceptors = append(c.requestInterceptors, interceptor)
}

// AddResponseInterceptor adds a response interceptor
func (c *Client) AddResponseInterceptor(interceptor ResponseInterceptor) {
	c.responseInterceptors = append(c.responseInterceptors, interceptor)
}

// Request makes an HTTP request
func (c *Client) Request(ctx context.Context, method, url string, body interface{}) ([]byte, error) {
	start := time.Now()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var bodyReader io.Reader
	if body != nil {
		switch v := body.(type) {
		case io.Reader:
			bodyReader = v
		case []byte:
			bodyReader = bytes.NewReader(v)
		case string:
			bodyReader = bytes.NewReader([]byte(v))
		default:
			jsonBody, err := json.Marshal(body)
			if err != nil {
				return nil, &ClientError{Op: "marshal request body", Err: err}
			}
			bodyReader = bytes.NewReader(jsonBody)
		}
	}

	var respBody []byte
	operation := func() error {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return &ClientError{Op: "rate limit", Err: err}
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return &ClientError{Op: "create request", Err: err}
		}

		for key, value := range c.headers {
			req.Header.Set(key, value)
		}

		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		// Copy the request before modifying it
		for _, interceptor := range c.requestInterceptors {
			if err := interceptor(req); err != nil {
				return &ClientError{Op: "request interceptor", Err: err}
			}
		}

		resp, err := c.Do(req)
		if err != nil {
			return &ClientError{Op: "do request", Err: err}
		}
		defer resp.Body.Close()

		// Copy the response before modifying it
		for _, interceptor := range c.responseInterceptors {
			if err := interceptor(resp); err != nil {
				return &ClientError{Op: "response interceptor", Err: err}
			}
		}

		respBody, err = io.ReadAll(resp.Body)
		if err != nil {
			return &ClientError{Op: "read response body", Err: err}
		}

		// Check for non-2xx response
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &ClientError{
				Op:   "non-2xx response",
				Err:  fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody)),
				Code: resp.StatusCode,
			}
		}

		return nil
	}

	err := backoff.Retry(operation, backoff.WithMaxRetries(c.backOff, uint64(c.retries)))
	c.collectMetrics(start, err)
	if err != nil {
		c.logger.Error("Request failed", "error", err, "method", method, "url", url)
		return nil, err
	}

	c.logger.Info("Request successful", "method", method, "url", url)
	return respBody, nil
}

// collectMetrics updates the client's metrics
func (c *Client) collectMetrics(start time.Time, err error) {
	atomic.AddInt64(&c.metrics.RequestCount, 1)
	atomic.AddInt64(&c.metrics.TotalLatency, int64(time.Since(start)))
	if err != nil {
		atomic.AddInt64(&c.metrics.ErrorCount, 1)
	}
}

// Get sends a GET request
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	return c.Request(ctx, http.MethodGet, url, nil)
}

func (c *Client) Post(ctx context.Context, url string, body interface{}) ([]byte, error) {
	return c.Request(ctx, http.MethodPost, url, body)
}

func (c *Client) Put(ctx context.Context, url string, body interface{}) ([]byte, error) {
	return c.Request(ctx, http.MethodPut, url, body)
}

func (c *Client) Patch(ctx context.Context, url string, body interface{}) ([]byte, error) {
	return c.Request(ctx, http.MethodPatch, url, body)
}

func (c *Client) Delete(ctx context.Context, url string) ([]byte, error) {
	return c.Request(ctx, http.MethodDelete, url, nil)
}

// Close cancels the base context and closes any idle connections
func (c *Client) Close() {
	c.cancelFunc()
	c.CloseIdleConnections()
}

// GetMetrics returns the current metrics
func (c *Client) GetMetrics() Metrics {
	return Metrics{
		RequestCount: atomic.LoadInt64(&c.metrics.RequestCount),
		ErrorCount:   atomic.LoadInt64(&c.metrics.ErrorCount),
		TotalLatency: atomic.LoadInt64(&c.metrics.TotalLatency),
	}
}

// defaultLogger is a simple implementation of the Logger interface
type defaultLogger struct{}

func (l *defaultLogger) Info(msg string, keyVals ...interface{}) {
	fmt.Printf("INFO: %s %v\n", msg, keyVals)
}

func (l *defaultLogger) Error(msg string, keyVals ...interface{}) {
	fmt.Printf("ERROR: %s %v\n", msg, keyVals)
}
