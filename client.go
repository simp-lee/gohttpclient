package gohttpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/simp-lee/retry"
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
	retryOptions         []retry.Option
	logInfoEnabled       bool
}

// Metrics holds metrics for the client
type Metrics struct {
	TotalLatency int64 // in nanoseconds
	RequestCount int64
	ErrorCount   int64
}

// RequestInterceptor is a function that can modify requests before they are sent
type RequestInterceptor func(*http.Request) error

// ResponseInterceptor is a function that can modify responses after they are received
type ResponseInterceptor func(*http.Response) error

// Logger is a simple interface for logging messages
type Logger interface {
	Info(msg string, keyVals ...interface{})
	Error(msg string, keyVals ...interface{})
	Warn(msg string, keyVals ...interface{})
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

// WithRetryTimes sets the number of retries for failed requests.
func WithRetryTimes(maxRetries int) ClientOption {
	return func(c *Client) {
		c.retryOptions = append(c.retryOptions, retry.WithTimes(maxRetries))
	}
}

// WithRetryCustomBackoff sets a custom backoff strategy for retries
func WithRetryCustomBackoff(backoff retry.Backoff) ClientOption {
	return func(c *Client) {
		c.retryOptions = append(c.retryOptions, retry.WithCustomBackoff(backoff))
	}
}

// WithRetryLinearBackoff sets a linear backoff strategy for retries
func WithRetryLinearBackoff(interval time.Duration) ClientOption {
	return func(c *Client) {
		c.retryOptions = append(c.retryOptions, retry.WithLinearBackoff(interval))
	}
}

// WithRetryConstantBackoff sets a constant backoff strategy for retries
func WithRetryConstantBackoff(interval time.Duration) ClientOption {
	return func(c *Client) {
		c.retryOptions = append(c.retryOptions, retry.WithConstantBackoff(interval))
	}
}

// WithRetryRandomIntervalBackoff sets a random interval backoff strategy for retries
func WithRetryRandomIntervalBackoff(minInterval, maxInterval time.Duration) ClientOption {
	return func(c *Client) {
		c.retryOptions = append(c.retryOptions, retry.WithRandomIntervalBackoff(minInterval, maxInterval))
	}
}

// WithRetryExponentialBackoff sets an exponential backoff strategy with jitter for retries
func WithRetryExponentialBackoff(initialInterval, maxInterval, maxJitter time.Duration) ClientOption {
	return func(c *Client) {
		c.retryOptions = append(c.retryOptions, retry.WithExponentialBackoff(initialInterval, maxInterval, maxJitter))
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
		c.retryOptions = append(c.retryOptions, retry.WithLogger(func(format string, args ...interface{}) {
			logger.Warn(format, args...)
		}))
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

// WithCustomTransport sets custom transport for the client
func WithCustomTransport(transport *http.Transport) ClientOption {
	return func(c *Client) {
		c.Transport = transport
	}
}

// WithLogInfoEnabled sets whether Info logging is enabled for the client
func WithLogInfoEnabled(enabled bool) ClientOption {
	return func(c *Client) {
		c.logInfoEnabled = enabled
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
		retryOptions: []retry.Option{
			retry.WithTimes(3),
			retry.WithExponentialBackoff(2*time.Second, 10*time.Second, 500*time.Millisecond),
		},
		logInfoEnabled: false,
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

// Request makes an HTTP request with the given method, URL, and body.
// It applies all request and response interceptors, and handles retries according to the client's retry options.
func (c *Client) Request(ctx context.Context, method, url string, body interface{}) ([]byte, error) {
	start := time.Now()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	bodyReader, err := createBodyReader(body)
	if err != nil {
		return nil, &ClientError{Op: "marshal request body", Err: err}
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

	// Add context to retry options
	retryOptions := append(c.retryOptions, retry.WithContext(ctx))

	err = retry.Do(operation, retryOptions...)
	c.collectMetrics(start, err)
	if err != nil {
		c.logger.Error("Request failed", "error", err, "method", method, "url", url)
		return nil, err
	}

	if c.logInfoEnabled {
		c.logger.Info("Request successful", "method", method, "url", url)
	}

	return respBody, nil
}

// createBodyReader creates a reader for the request body
func createBodyReader(body interface{}) (io.Reader, error) {
	if body == nil {
		return nil, nil
	}
	switch v := body.(type) {
	case io.Reader:
		return v, nil
	case []byte:
		return bytes.NewReader(v), nil
	case string:
		return bytes.NewReader([]byte(v)), nil
	default:
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(jsonBody), nil
	}
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
	if c.logInfoEnabled {
		c.logger.Info("Client closed")
	}
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

func (l *defaultLogger) Warn(msg string, keyVals ...interface{}) {
	fmt.Printf("WARN: %s %v\n", msg, keyVals)
}
