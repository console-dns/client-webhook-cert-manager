package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	sdkUserAgent    = "CoreDNS-plugin-console"
	maxResponseBody = 16 << 20
)

// HTTPOptions controls the process-wide HTTP bridge used by the pinned
// console-dns client. The SDK has no way to receive an http.Client, so this
// bridge is installed before the webhook starts serving.
type HTTPOptions struct {
	Timeout               time.Duration
	InsecureSkipTLSVerify bool
	CAFile                string
}

var httpConfigState struct {
	sync.Mutex
	configured bool
}

// ConfigureHTTP installs the SDK-aware default HTTP client once. Requests
// emitted by the pinned SDK are identified by its fixed User-Agent. All other
// requests retain the original client's transport and redirect policy.
func ConfigureHTTP(opts HTTPOptions) error {
	if opts.Timeout <= 0 {
		return errors.New("provider: HTTP timeout must be positive")
	}
	if opts.CAFile != "" && opts.InsecureSkipTLSVerify {
		// This combination is valid in crypto/tls, but accepting a CA file and
		// explicitly disabling verification is almost certainly a configuration
		// mistake. More importantly, it makes the intended trust policy opaque.
		return errors.New("provider: CAFile cannot be combined with insecure TLS verification")
	}

	var roots *x509.CertPool
	if opts.CAFile != "" {
		pem, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return fmt.Errorf("provider: read CA file: %w", err)
		}
		roots, _ = x509.SystemCertPool()
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("provider: CA file contains no certificates")
		}
	}

	httpConfigState.Lock()
	defer httpConfigState.Unlock()
	if httpConfigState.configured {
		return errors.New("provider: HTTP client is already configured")
	}

	original := http.DefaultClient
	if original == nil {
		original = &http.Client{}
	}
	originalTransport := original.Transport
	if originalTransport == nil {
		originalTransport = http.DefaultTransport
	}

	sdkTransport, err := sdkTransport(originalTransport, opts, roots)
	if err != nil {
		return err
	}
	bridge := &httpBridge{
		other:   originalTransport,
		sdk:     sdkTransport,
		timeout: opts.Timeout,
	}

	configured := *original
	configured.Transport = bridge
	configured.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		sdkRedirect := isSDKRequest(req)
		if !sdkRedirect {
			for _, previous := range via {
				if isSDKRequest(previous) {
					sdkRedirect = true
					break
				}
			}
		}
		if sdkRedirect {
			return errors.New("provider: redirects are disabled for console-dns requests")
		}
		if original.CheckRedirect != nil {
			return original.CheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	http.DefaultClient = &configured
	httpConfigState.configured = true
	return nil
}

func sdkTransport(base http.RoundTripper, opts HTTPOptions, roots *x509.CertPool) (http.RoundTripper, error) {
	if !opts.InsecureSkipTLSVerify && roots == nil {
		return base, nil
	}
	transport, ok := base.(*http.Transport)
	if !ok {
		return nil, errors.New("provider: custom TLS settings require an *http.Transport")
	}
	clone := transport.Clone()
	tlsConfig := clone.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.InsecureSkipVerify = opts.InsecureSkipTLSVerify // explicit user option
	if roots != nil {
		tlsConfig.RootCAs = roots
	}
	clone.TLSClientConfig = tlsConfig
	return clone, nil
}

type httpBridge struct {
	other   http.RoundTripper
	sdk     http.RoundTripper
	timeout time.Duration
}

func (b *httpBridge) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("provider: nil HTTP request")
	}
	if !isSDKRequest(req) {
		return b.other.RoundTrip(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), b.timeout)
	defer cancel()
	resp, err := b.sdk.RoundTrip(req.WithContext(ctx))
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	body, err := readBounded(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

func readBounded(body io.ReadCloser) ([]byte, error) {
	// net/http cancels body reads when the request context expires. Keep the
	// read in this goroutine so no request work survives an early return.
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBody {
		return nil, fmt.Errorf("provider: HTTP response body exceeds %d bytes", maxResponseBody)
	}
	return data, nil
}

func isSDKRequest(req *http.Request) bool {
	return req != nil && strings.TrimSpace(req.Header.Get("User-Agent")) == sdkUserAgent
}
