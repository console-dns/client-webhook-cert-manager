package provider

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	consoleclient "github.com/console-dns/client"
)

func resetHTTPForTest(client *http.Client) {
	httpConfigState.Lock()
	defer httpConfigState.Unlock()
	http.DefaultClient = client
	httpConfigState.configured = false
}

func TestConfigureHTTPRestrictsTLSSkipToSDK(t *testing.T) {
	original := http.DefaultClient
	defer resetHTTPForTest(original)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == sdkUserAgent {
			_, _ = io.WriteString(w, `{"records":{}}`)
			return
		}
		_, _ = io.WriteString(w, "ordinary")
	}))
	defer server.Close()

	if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	sdk := consoleclient.NewConsoleDnsClient(server.URL, "secret")
	if _, _, err := sdk.ListZone("example.com"); err == nil {
		t.Fatal("SDK unexpectedly accepted an untrusted certificate")
	}
	if _, err := http.DefaultClient.Get(server.URL); err == nil {
		t.Fatal("ordinary request unexpectedly accepted an untrusted certificate")
	}

	resetHTTPForTest(original)
	if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second, InsecureSkipTLSVerify: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sdk.ListZone("example.com"); err != nil {
		t.Fatalf("SDK did not honor explicit TLS skip: %v", err)
	}
	if _, err := http.DefaultClient.Get(server.URL); err == nil {
		t.Fatal("ordinary request inherited SDK TLS skip")
	}
}

func TestConfigureHTTPWithCAFile(t *testing.T) {
	original := http.DefaultClient
	defer resetHTTPForTest(original)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"records":{}}`)
	}))
	defer server.Close()
	certFile := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second, CAFile: certFile}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := consoleclient.NewConsoleDnsClient(server.URL, "secret").ListZone("example.com"); err != nil {
		t.Fatalf("SDK did not trust configured CA: %v", err)
	}
}

func TestConfigureHTTPValidationAndOnce(t *testing.T) {
	original := http.DefaultClient
	defer resetHTTPForTest(original)
	for _, options := range []HTTPOptions{{}, {Timeout: -time.Second}, {Timeout: time.Second, CAFile: "/does/not/exist"}, {Timeout: time.Second, InsecureSkipTLSVerify: true, CAFile: "/tmp/ca"}} {
		if err := ConfigureHTTP(options); err == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}
	if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second}); err == nil {
		t.Fatal("allowed HTTP client reconfiguration")
	}
}

func TestSDKTimeoutRedirectAndTruncatedBody(t *testing.T) {
	t.Run("body timeout", func(t *testing.T) {
		original := http.DefaultClient
		defer resetHTTPForTest(original)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer server.Close()
		if err := ConfigureHTTP(HTTPOptions{Timeout: 25 * time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := consoleclient.NewConsoleDnsClient(server.URL, "secret").ListZone("example.com"); err == nil {
			t.Fatal("accepted an indefinitely stalled response body")
		}
	})

	t.Run("body limit", func(t *testing.T) {
		original := http.DefaultClient
		defer resetHTTPForTest(original)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBody+1))
		}))
		defer server.Close()
		if err := ConfigureHTTP(HTTPOptions{Timeout: 5 * time.Second}); err != nil {
			t.Fatal(err)
		}
		_, _, err := consoleclient.NewConsoleDnsClient(server.URL, "secret").ListZone("example.com")
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("expected response size error, got %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		original := http.DefaultClient
		defer resetHTTPForTest(original)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			_, _ = io.WriteString(w, `{"records":{}}`)
		}))
		defer server.Close()
		if err := ConfigureHTTP(HTTPOptions{Timeout: 25 * time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		_, _, err := consoleclient.NewConsoleDnsClient(server.URL, "secret").ListZone("example.com")
		if err == nil || !strings.Contains(err.Error(), "deadline") {
			t.Fatalf("expected deadline error, got %v", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		original := http.DefaultClient
		defer resetHTTPForTest(original)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"records":{}}`)
		}))
		defer target.Close()
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer redirect.Close()
		if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second}); err != nil {
			t.Fatal(err)
		}
		_, _, err := consoleclient.NewConsoleDnsClient(redirect.URL, "secret").ListZone("example.com")
		if err == nil || !strings.Contains(err.Error(), "redirect") {
			t.Fatalf("expected blocked redirect, got %v", err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		original := http.DefaultClient
		defer resetHTTPForTest(original)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "20")
			_, _ = io.WriteString(w, "short")
		}))
		defer server.Close()
		if err := ConfigureHTTP(HTTPOptions{Timeout: time.Second}); err != nil {
			t.Fatal(err)
		}
		_, _, err := consoleclient.NewConsoleDnsClient(server.URL, "secret").ListZone("example.com")
		if err == nil {
			t.Fatal("accepted truncated SDK response")
		}
	})
}
