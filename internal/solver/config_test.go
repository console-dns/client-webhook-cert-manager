package solver

import (
	"fmt"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(&apiextensionsv1.JSON{Raw: []byte(`{"server":"https://dns.example.com/console/","tokenSecretRef":{"name":"api-token","key":"token"}}`)})
	if err != nil || cfg.Server != "https://dns.example.com/console" || cfg.TTL == nil || *cfg.TTL != 60 {
		t.Fatalf("unexpected config: %+v %v", cfg, err)
	}
	for _, raw := range []string{
		``, `null`, `[]`, `{`, `{}`, `{"token":"secret"}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"api-token","key":"token","namespace":"other"}}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"api-token","key":"token"},"ttl":0}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"api-token","key":"token"},"ttl":86401}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"api-token","key":"token"},"ttl":-1}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"api-token","key":"token"}} {}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"INVALID","key":"token"}}`,
		`{"server":"https://dns.example.com","tokenSecretRef":{"name":"api-token","key":"../token"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := loadConfig(&apiextensionsv1.JSON{Raw: []byte(raw)}); err == nil {
				t.Fatalf("accepted invalid config %s", raw)
			}
		})
	}
	if _, err := loadConfig(nil); err == nil {
		t.Fatal("accepted missing config")
	}
}

func TestNormalizeServer(t *testing.T) {
	for input, want := range map[string]string{
		"https://dns.example.com/":       "https://dns.example.com",
		"http://localhost:8090/console/": "http://localhost:8090/console",
		"https://[::1]:8443":             "https://[::1]:8443",
	} {
		got, err := normalizeServer(input)
		if err != nil || got != want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"", "dns.example.com", "file:///tmp/dns", "https://", "https://dns.example.com:0", "https://dns.example.com:65536",
		"https://user:private-token@dns.example.com", "https://dns.example.com?token=private-token", "https://dns.example.com?",
		"https://dns.example.com#fragment", "https://dns.example.com/a/../b", "https://dns.example.com/%2e%2e",
		"https://dns.example.com//path", "https://dns.example.com/with space", "https://dns.example.com/\r\n",
	} {
		_, err := normalizeServer(input)
		if err == nil {
			t.Errorf("accepted invalid server %q", input)
		} else if strings.Contains(err.Error(), "private-token") {
			t.Errorf("error exposed credential: %v", err)
		}
	}
}

func TestRecordLocation(t *testing.T) {
	for _, tt := range []struct{ fqdn, zone, wantZone, wantName string }{
		{"_acme-challenge.example.com.", "example.com.", "example.com", "_acme-challenge"},
		{"_acme-challenge.www.example.com.", "example.com.", "example.com", "_acme-challenge.www"},
		{"_ACME-CHALLENGE.EXAMPLE.COM.", "EXAMPLE.COM.", "example.com", "_acme-challenge"},
		{"validation.delegated.example.net.", "delegated.example.net.", "delegated.example.net", "validation"},
	} {
		zone, name, err := recordLocation(tt.fqdn, tt.zone)
		if err != nil || zone != tt.wantZone || name != tt.wantName {
			t.Errorf("recordLocation(%q,%q) = %q,%q,%v", tt.fqdn, tt.zone, zone, name, err)
		}
	}
	for _, tt := range [][2]string{
		{"_acme-challenge.badexample.com.", "example.com."},
		{"example.com.", "example.com."}, {"", "example.com."}, {"_acme-challenge.example.com.", ""},
		{"_acme-challenge.localhost.", "localhost."}, {"../example.com.", "example.com."},
		{"*.example.com.", "example.com."}, {"a..example.com.", "example.com."},
		{"-bad.example.com.", "example.com."}, {"bad-.example.com.", "example.com."},
		{fmt.Sprintf("%s.example.com.", strings.Repeat("a", 64)), "example.com."},
		{strings.Repeat("longlabel.", 30) + "example.com.", "example.com."},
	} {
		if _, _, err := recordLocation(tt[0], tt[1]); err == nil {
			t.Errorf("accepted invalid name pair %v", tt)
		}
	}
}
