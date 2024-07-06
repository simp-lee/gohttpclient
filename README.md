# GoHTTPClient

`gohttpclient` is a custom HTTP client library for Go, designed to provide enhanced functionality and optimizations over the standard `net/http` client. This library includes features such as request and response interceptors, rate limiting, retry mechanisms with exponential backoff, and detailed metrics tracking.

## Features

- **Request and Response Interceptors**: Modify requests before they are sent and responses after they are received.
- **Rate Limiting**: Control the rate of HTTP requests to prevent overwhelming the server.
- **Retry Mechanisms**: Automatically retry failed requests with exponential backoff.
- **Metrics Tracking**: Track request count, error count, and total latency.
- **Customizable Transport Settings**: Fine-tune settings such as timeouts, keep-alives, and more.
- **Logging**: Built-in logging support with customizable logger.
- **Proxy Support**: Configure HTTP and HTTPS proxies for your requests.
- **Supports most standard HTTP methods**: GET, POST, PUT, PATCH, DELETE.

## Installation

To install the `gohttpclient` library, use `go get`:

```sh
go get github.com/simp-lee/gohttpclient
```

## Quick Start

Here's a basic example of how to use the `gohttpclient` library:

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

### Customizing the Client

You can customize the client behavior using various options:

```go
client := gohttpclient.NewClient(
	gohttpclient.WithTimeout(10 * time.Second),
	gohttpclient.WithRetries(3),
	gohttpclient.WithRateLimit(10, 5),
	gohttpclient.WithProxy(url.Parse("http://proxy.example.com:8080")),
	gohttpclient.WithLogger(&customLogger{}),
	gohttpclient.WithMaxIdleConns(100),
	gohttpclient.WithMaxConnsPerHost(10),
	gohttpclient.WithIdleConnTimeout(30 * time.Second),
	gohttpclient.WithTLSHandshakeTimeout(5 * time.Second),
	gohttpclient.WithDisableKeepAlives(false),
	gohttpclient.WithDisableCompression(false),
)
```

### Adding Interceptors

You can add request and response interceptors to modify requests and responses:

```go
client.AddRequestInterceptor(func(req *http.Request) error {
	req.Header.Set("X-Custom-Header", "value")
	req.Header.Set("Authorization", "Bearer some-token")
	return nil
})

client.AddResponseInterceptor(func(resp *http.Response) error {
	fmt.Println("Response status:", resp.Status)
	// Do something with the response
	return nil
})
```

### Metrics Tracking

You can retrieve metrics about the client's activity:

```go
metrics := client.GetMetrics()
fmt.Printf("Request Count: %d\n", metrics.RequestCount)
fmt.Printf("Error Count: %d\n", metrics.ErrorCount)
fmt.Printf("Total Latency: %v\n", time.Duration(metrics.TotalLatency))
```

## Contributing

Contributions are welcome! Please open an issue or submit a pull request for any improvements or bug fixes.

## License

This project is licensed under the MIT License.