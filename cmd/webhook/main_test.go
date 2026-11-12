package main

import (
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	getenv := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	group, opts, err := configuration(getenv(nil))
	if err != nil || group != "acme.console-dns.github.com" || opts.Timeout != 10*time.Second || opts.InsecureSkipTLSVerify {
		t.Fatalf("unexpected defaults: %s %+v %v", group, opts, err)
	}
	group, opts, err = configuration(getenv(map[string]string{
		"GROUP_NAME": "acme.example.com", "DNS_API_TIMEOUT": "5s",
		"DNS_API_INSECURE_SKIP_TLS_VERIFY": "true", "DNS_API_CA_FILE": "/ca/ca.crt",
	}))
	if err != nil || group != "acme.example.com" || opts.Timeout != 5*time.Second || !opts.InsecureSkipTLSVerify || opts.CAFile != "/ca/ca.crt" {
		t.Fatalf("unexpected configured values: %s %+v %v", group, opts, err)
	}
	for _, values := range []map[string]string{
		{"GROUP_NAME": "invalid/group"}, {"DNS_API_TIMEOUT": "0s"},
		{"DNS_API_TIMEOUT": "-1s"}, {"DNS_API_TIMEOUT": "2m"},
		{"DNS_API_TIMEOUT": "invalid"}, {"DNS_API_INSECURE_SKIP_TLS_VERIFY": "invalid"},
	} {
		if _, _, err := configuration(getenv(values)); err == nil {
			t.Fatalf("accepted invalid environment: %v", values)
		}
	}
}
