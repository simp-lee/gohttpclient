package gohttpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/simp-lee/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockLogger is a simple implementation of the Logger interface for testing
type mockLogger struct {
	mu        sync.Mutex
	InfoLogs  []string
	ErrorLogs []string
}

func (l *mockLogger) Info(msg string, keyVals ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.InfoLogs = append(l.InfoLogs, msg)

	slog.Info(fmt.Sprintf(msg, keyVals...))
}

func (l *mockLogger) Error(msg string, keyVals ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ErrorLogs = append(l.ErrorLogs, msg)

	slog.Error(fmt.Sprintf(msg, keyVals...))
}

func (l *mockLogger) Warn(msg string, keyVals ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	slog.Warn(fmt.Sprintf(msg, keyVals...))
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name     string
		options  []ClientOption
		expected Client
	}{
		{
			name: "Default client",
			expected: Client{
				Client: http.Client{
					Timeout: 30 * time.Second,
				},
				retryOptions: []retry.Option{
					retry.WithTimes(3),
					retry.WithExponentialBackoff(2*time.Second, 10*time.Second, 500*time.Millisecond),
				},
				logger: &defaultLogger{},
			},
		},
		{
			name: "Custom options",
			options: []ClientOption{
				WithTimeout(10 * time.Second),
				WithRetryTimes(5),
				WithRateLimit(1, 5),
				WithLogger(&mockLogger{}),
			},
			expected: Client{
				Client: http.Client{
					Timeout: 10 * time.Second,
				},
				retryOptions: []retry.Option{
					retry.WithTimes(5),
				},
				limiter: rate.NewLimiter(rate.Limit(1), 5),
				logger:  &mockLogger{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.options...)
			assert.Equal(t, tt.expected.Timeout, client.Timeout)

			for i, expectedOption := range tt.expected.retryOptions {
				assert.Equal(t, reflect.TypeOf(expectedOption), reflect.TypeOf(client.retryOptions[i]))
			}

			assert.Equal(t, tt.expected.logger, client.logger)
			if tt.expected.limiter != nil {
				assert.NotNil(t, client.limiter)
				assert.Equal(t, tt.expected.limiter.Limit(), client.limiter.Limit())
				assert.Equal(t, tt.expected.limiter.Burst(), client.limiter.Burst())
			} else {
				assert.Nil(t, client.limiter)
			}
			assert.NotNil(t, client.logger)
			if tt.name == "Custom options" {
				assert.NotNil(t, client.limiter)
			}
		})
	}
}

func TestClient_Request(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		body           interface{}
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expected       []byte
		expectedError  bool
		logSuccess     bool
	}{
		{
			name:   "Successful GET request",
			method: http.MethodGet,
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"message": "success"}`))
			},
			expected:   []byte(`{"message": "success"}`),
			logSuccess: true,
		},
		{
			name:   "Successful POST request with body",
			method: http.MethodPost,
			body:   map[string]string{"key": "value"},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				defer r.Body.Close()
				assert.Equal(t, []byte(`{"key":"value"}`), body)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"message": "success"}`))
			},
			expected:   []byte(`{"message": "success"}`),
			logSuccess: true,
		},
		{
			name:   "Server error",
			method: http.MethodGet,
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message": "error"}`))
			},
			expectedError: true,
			logSuccess:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			logger := &mockLogger{}
			client := NewClient(WithLogger(logger), WithLogInfoEnabled(tt.logSuccess))
			defer client.Close()
			ctx := context.Background()
			respBody, err := client.Request(ctx, tt.method, server.URL, tt.body)

			if tt.expectedError {
				require.Error(t, err)
				assert.True(t, strings.HasPrefix(logger.ErrorLogs[0], "Request failed"))
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, respBody)
				if tt.logSuccess {
					assert.True(t, len(logger.InfoLogs) > 0 && strings.HasPrefix(logger.InfoLogs[0], "Request successful"))
				} else {
					assert.Empty(t, logger.InfoLogs)
				}
			}
		})
	}
}

func TestClient_RequestWithRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"message": "success"}`))
		}
	}))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(
		WithRetryTimes(3),
		WithRetryLinearBackoff(10*time.Millisecond),
		WithLogger(logger),
		WithLogInfoEnabled(true),
	)
	defer client.Close()
	ctx := context.Background()
	respBody, err := client.Request(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	expected := []byte(`{"message": "success"}`)
	assert.Equal(t, expected, respBody)
	assert.Equal(t, 3, attempts)
	assert.True(t, strings.HasPrefix(logger.InfoLogs[0], "Request successful"))
}

func TestClient_RequestWithError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "error"}`))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(WithLogger(logger), WithLogInfoEnabled(true))
	defer client.Close()

	ctx := context.Background()
	_, err := client.Request(ctx, http.MethodGet, server.URL, nil)
	require.Error(t, err)

	require.Contains(t, err.Error(), "HTTP 500")
	assert.True(t, strings.HasPrefix(logger.ErrorLogs[0], "Request failed"))
	assert.Empty(t, logger.InfoLogs)
}

func TestClient_RequestWithInterceptor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-value", r.Header.Get("X-Test-Header"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "interceptor success"}`))
	}))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(WithLogger(logger), WithLogInfoEnabled(true))
	defer client.Close()

	client.AddRequestInterceptor(func(req *http.Request) error {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Header", "test-value")
		return nil
	})

	ctx := context.Background()
	respBody, err := client.Request(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	expected := []byte(`{"message": "interceptor success"}`)
	assert.Equal(t, expected, respBody)

	assert.True(t, strings.HasPrefix(logger.InfoLogs[0], "Request successful"))
}

func TestClient_RequestWithRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "success"}`))
	}))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(
		WithRateLimit(10, 1),
		WithLogger(logger),
		WithLogInfoEnabled(true),
	)
	defer client.Close()

	start := time.Now()
	for i := 0; i < 5; i++ {
		ctx := context.Background()
		_, err := client.Request(ctx, http.MethodGet, server.URL, nil)
		require.NoError(t, err)
	}
	duration := time.Since(start)

	assert.True(t, duration >= 400*time.Millisecond, "Rate limiting should slow down requests")
	assert.Equal(t, 5, len(logger.InfoLogs))
	for _, log := range logger.InfoLogs {
		assert.True(t, strings.HasPrefix(log, "Request successful"))
	}
}

func TestClient_RequestWithCustomBody(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		expectedBody := []byte(`{"custom":"body"}`)
		assert.Equal(t, expectedBody, body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "custom body success"}`))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(WithLogger(logger), WithLogInfoEnabled(true))
	defer client.Close()

	body := map[string]string{"custom": "body"}

	ctx := context.Background()
	respBody, err := client.Request(ctx, http.MethodPost, server.URL, body)
	require.NoError(t, err)

	expected := []byte(`{"message": "custom body success"}`)
	assert.Equal(t, expected, respBody)
	assert.True(t, strings.HasPrefix(logger.InfoLogs[0], "Request successful"))
}

func TestClient_Close(t *testing.T) {
	logger := &mockLogger{}
	client := NewClient(WithLogger(logger), WithLogInfoEnabled(true))
	client.Close()
	assert.NotNil(t, client)
	assert.True(t, strings.HasPrefix(logger.InfoLogs[0], "Client closed"))
}

func TestClient_Get(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "GET success"}`))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(WithLogger(logger), WithLogInfoEnabled(true))
	defer client.Close()

	ctx := context.Background()
	respBody, err := client.Get(ctx, server.URL)
	require.NoError(t, err)

	expected := []byte(`{"message": "GET success"}`)
	assert.Equal(t, expected, respBody)
	assert.True(t, strings.HasPrefix(logger.InfoLogs[0], "Request successful"))
}

func TestClient_Post(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		expectedBody := []byte(`{"key":"value"}`)
		assert.Equal(t, expectedBody, body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "POST success"}`))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	logger := &mockLogger{}
	client := NewClient(WithLogger(logger), WithLogInfoEnabled(true))
	defer client.Close()
	body := map[string]string{"key": "value"}

	ctx := context.Background()
	respBody, err := client.Post(ctx, server.URL, body)
	require.NoError(t, err)

	expected := []byte(`{"message": "POST success"}`)
	assert.Equal(t, expected, respBody)
	assert.True(t, strings.HasPrefix(logger.InfoLogs[0], "Request successful"))
}

func TestClient_Metrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		w.Write([]byte(`{"message": "metrics"}`))
	}))
	defer server.Close()

	client := NewClient(WithLogger(&mockLogger{}))
	defer client.Close()
	ctx := context.Background()

	// Successful request
	_, err := client.Request(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	// Error request
	_, err = client.Request(ctx, http.MethodGet, server.URL+"/error", nil)
	require.Error(t, err)

	metrics := client.GetMetrics()
	assert.Equal(t, int64(2), metrics.RequestCount)
	assert.Equal(t, int64(1), metrics.ErrorCount)
	assert.True(t, metrics.TotalLatency > 0)
}

func TestClient_WithProxy(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.String(), "/proxy-test")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "proxy success"}`))
	}))
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := NewClient(
		WithProxy(proxyURL),
		WithLogger(&mockLogger{}),
	)
	defer client.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Request should not reach this server")
	}))
	defer server.Close()

	ctx := context.Background()
	respBody, err := client.Request(ctx, http.MethodGet, server.URL+"/proxy-test", nil)
	require.NoError(t, err)

	expected := []byte(`{"message": "proxy success"}`)
	assert.Equal(t, expected, respBody)
}

func TestClient_ConcurrentRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "success"}`))
	}))
	defer server.Close()

	client := NewClient(
		WithMaxConnsPerHost(5),
		WithLogger(&mockLogger{}),
	)
	defer client.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, err := client.Request(ctx, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
		}()
	}

	wg.Wait()

	metrics := client.GetMetrics()
	assert.Equal(t, int64(10), metrics.RequestCount)
	assert.Equal(t, int64(0), metrics.ErrorCount)
}

func TestClient_Convenience_Methods(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   interface{}
	}{
		{"GET", http.MethodGet, nil},
		{"POST", http.MethodPost, map[string]string{"key": "value"}},
		{"PUT", http.MethodPut, map[string]string{"key": "value"}},
		{"PATCH", http.MethodPatch, map[string]string{"key": "value"}},
		{"DELETE", http.MethodDelete, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.method, r.Method)
				if tt.body != nil {
					body, _ := io.ReadAll(r.Body)
					defer r.Body.Close()
					expectedBody, _ := json.Marshal(tt.body)
					assert.Equal(t, expectedBody, body)
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"message": "success"}`))
			}))
			defer server.Close()

			client := NewClient(WithLogger(&mockLogger{}))
			defer client.Close()
			ctx := context.Background()

			var respBody []byte
			var err error

			switch tt.method {
			case http.MethodGet:
				respBody, err = client.Get(ctx, server.URL)
			case http.MethodPost:
				respBody, err = client.Post(ctx, server.URL, tt.body)
			case http.MethodPut:
				respBody, err = client.Put(ctx, server.URL, tt.body)
			case http.MethodPatch:
				respBody, err = client.Patch(ctx, server.URL, tt.body)
			case http.MethodDelete:
				respBody, err = client.Delete(ctx, server.URL)
			}

			require.NoError(t, err)
			expected := []byte(`{"message": "success"}`)
			assert.Equal(t, expected, respBody)
		})
	}
}
