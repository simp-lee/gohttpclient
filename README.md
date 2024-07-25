# GoHTTPClient

`gohttpclient` is a powerful and flexible HTTP client library for Go, designed to enhance and extend the functionality of the standard `net/http` client. It provides a robust set of features including interceptors, rate limiting, automatic retries, and detailed metrics tracking, making it ideal for building resilient and observable network applications.

## Table of Contents
- [Features](#features)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Advanced Usage](#advanced-usage)
  - [Client Customization](#client-customization)
  - [Interceptors](#interceptors)
  - [Retry Mechanisms](#retry-mechanisms)
  - [Metrics Tracking](#metrics-tracking)
  - [Custom Logging](#custom-logging)
- [Best Practices](#best-practices)
- [API Reference](#api-reference)
- [Contributing](#contributing)
- [License](#license)


## Features

- **Enhanced HTTP Methods**: Support for GET, POST, PUT, PATCH, DELETE with easy-to-use interfaces.
- **Request/Response Interceptors**: Modify requests before sending and responses after receiving.
- **Rate Limiting**: Control request rates to prevent overwhelming servers.
- **Automatic Retries**: Configurable retry mechanisms with various backoff strategies.
- **Metrics Tracking**: Monitor request counts, error rates, and latencies.
- **Customizable Transport**: Fine-tune connection pooling, timeouts, and more.
- **Logging**: Built-in logging support with customizable logger interface.
- **Proxy Support**: Easy configuration of HTTP and HTTPS proxies.

## Installation

To install `gohttpclient`, use `go get`:

```sh
go get github.com/simp-lee/gohttpclient
```

## Quick Start

Here's a simple example to get you started:

```go
package main

import (
	"context"
	"fmt"
	"github.com/simp-lee/gohttpclient"
)

func main() {
	client := gohttpclient.NewClient()

	resp, err := client.Get(context.Background(), "https://api.example.com/data")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Response:", string(resp))
}
```

## Advanced Usage

### Client Customization

Customize the client behavior using various options:

```go
client := gohttpclient.NewClient(
	gohttpclient.WithTimeout(10 * time.Second),
	gohttpclient.WithRetryTimes(3),
	gohttpclient.WithRateLimit(10, 5),
	gohttpclient.WithMaxIdleConns(100),
	gohttpclient.WithMaxConnsPerHost(10),
)
```

### Interceptors

Add request and response interceptors:

```go
client.AddRequestInterceptor(func(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer token")
	return nil
})

client.AddResponseInterceptor(func(resp *http.Response) error {
	fmt.Println("Response status:", resp.Status)
	return nil
})
```

### Retry Mechanisms

`gohttpclient` uses the `github.com/simp-lee/retry` library to implement flexible retry mechanisms. You can configure various backoff strategies to suit your needs:

```go
client := gohttpclient.NewClient(
	gohttpclient.WithRetryTimes(3),
	gohttpclient.WithRetryExponentialBackoff(1*time.Second, 30*time.Second, 100*time.Millisecond),
)
```

Supported backoff strategies include:

- **Linear Backoff**: Increases the delay between retries linearly.

```go
gohttpclient.WithRetryLinearBackoff(2 * time.Second)
```

- **Constant Backoff**: Uses a fixed delay between retries.

```go
gohttpclient.WithRetryConstantBackoff(5 * time.Second)
```

- **Exponential Backoff with Jitter**: Increases the delay exponentially and adds random jitter to prevent thundering herd problems.

```go
gohttpclient.WithRetryExponentialBackoff(1*time.Second, 30*time.Second, 100*time.Millisecond)
```

- **Random Interval Backoff**: Uses a random delay between a minimum and maximum duration.

```go
gohttpclient.WithRetryRandomIntervalBackoff(1*time.Second, 5*time.Second)
```

- **Custom Backoff Strategy**: Implement your own backoff strategy. Refer to the [github.com/simp-lee/retry](https://github.com/simp-lee/retry) documentation for details.

```go
gohttpclient.WithRetryCustomBackoff(myCustomBackoffFunc)
```

### Metrics Tracking

Retrieve client metrics:

```go
metrics := client.GetMetrics()
fmt.Printf("Requests: %d, Errors: %d, Total Latency: %v\n", 
	metrics.RequestCount, metrics.ErrorCount, 
	time.Duration(metrics.TotalLatency))
```

### Custom Logging

Implement custom logging:

```go
type customLogger struct{}

func (l *customLogger) Info(msg string, keyVals ...interface{})  { /* implementation */ }
func (l *customLogger) Error(msg string, keyVals ...interface{}) { /* implementation */ }
func (l *customLogger) Warn(msg string, keyVals ...interface{})  { /* implementation */ }

client := gohttpclient.NewClient(gohttpclient.WithLogger(&customLogger{}))
```

## Best Practices

1. Always use context for proper cancellation and timeout handling.
2. Implement circuit breaking for resilience against failing dependencies.
3. Use interceptors for cross-cutting concerns like authentication or logging.
4. Monitor and analyze metrics to optimize client performance.
5. Configure appropriate timeouts and retry mechanisms for your use case.

## API Reference

### Client Creation

- `NewClient(options ...ClientOption) *Client` Creates a new GoHTTPClient with the specified options.

### HTTP Methods

- `Get(ctx context.Context, url string) ([]byte, error)`
- `Post(ctx context.Context, url string, body interface{}) ([]byte, error)`
- `Put(ctx context.Context, url string, body interface{}) ([]byte, error)`
- `Patch(ctx context.Context, url string, body interface{}) ([]byte, error)`
- `Delete(ctx context.Context, url string) ([]byte, error)`

### Client Options

- `WithTimeout(timeout time.Duration) ClientOption`
- `WithRateLimit(rps float64, burst int) ClientOption`
- `WithLogger(logger Logger) ClientOption`
- `WithMaxIdleConns(n int) ClientOption`
- `WithMaxConnsPerHost(n int) ClientOption`
- `WithIdleConnTimeout(d time.Duration) ClientOption`
- `WithTLSHandshakeTimeout(d time.Duration) ClientOption`
- `WithDisableKeepAlives(disable bool) ClientOption`
- `WithDisableCompression(disable bool) ClientOption`
- `WithProxy(proxyURL *url.URL) ClientOption`
- `WithCustomTransport(transport *http.Transport) ClientOption`
- `WithLogInfoEnabled(enabled bool) ClientOption`

### Retry Options

- `WithRetryTimes(maxRetries int) ClientOption`
- `WithRetryLinearBackoff(interval time.Duration) ClientOption`
- `WithRetryConstantBackoff(interval time.Duration) ClientOption`
- `WithRetryExponentialBackoff(initialInterval, maxInterval, maxJitter time.Duration) ClientOption`
- `WithRetryRandomIntervalBackoff(minInterval, maxInterval time.Duration) ClientOption`
- `WithRetryCustomBackoff(backoff retry.Backoff) ClientOption`

### Interceptors

- `AddRequestInterceptor(interceptor RequestInterceptor)`
- `AddResponseInterceptor(interceptor ResponseInterceptor)`

### Metric

- `GetMetrics() Metrics`

### Other Methods

- `SetHeader(key, value string)`
- `Close()`

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License.