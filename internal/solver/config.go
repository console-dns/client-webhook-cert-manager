package solver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const defaultTTL uint32 = 60

type config struct {
	Server         string                   `json:"server"`
	TokenSecretRef cmmeta.SecretKeySelector `json:"tokenSecretRef"`
	TTL            *uint32                  `json:"ttl,omitempty"`
}

func loadConfig(raw *apiextensionsv1.JSON) (config, error) {
	var cfg config
	if raw == nil || len(raw.Raw) == 0 {
		return cfg, fmt.Errorf("solver config is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw.Raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid solver config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return cfg, fmt.Errorf("solver config must contain a single JSON object")
	}
	server, err := normalizeServer(cfg.Server)
	if err != nil {
		return cfg, err
	}
	cfg.Server = server
	if cfg.TokenSecretRef.Name == "" || len(validation.IsDNS1123Subdomain(cfg.TokenSecretRef.Name)) != 0 {
		return cfg, fmt.Errorf("tokenSecretRef.name must be a valid Secret name")
	}
	if cfg.TokenSecretRef.Key == "" || len(validation.IsConfigMapKey(cfg.TokenSecretRef.Key)) != 0 {
		return cfg, fmt.Errorf("tokenSecretRef.key must be a valid Secret key")
	}
	if cfg.TTL == nil {
		ttl := defaultTTL
		cfg.TTL = &ttl
	}
	if *cfg.TTL < 1 || *cfg.TTL > 86400 {
		return cfg, fmt.Errorf("ttl must be between 1 and 86400 seconds")
	}
	return cfg, nil
}

func normalizeServer(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("server must be an absolute http or https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return "", fmt.Errorf("server URL must not contain credentials, query, fragment, or escaped path segments")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", fmt.Errorf("server URL port must be between 1 and 65535")
		}
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("server URL must not contain relative path segments")
		}
	}
	if strings.Contains(u.Path, "//") || strings.ContainsAny(u.Path, "\\ \t\r\n") {
		return "", fmt.Errorf("server URL path is invalid")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}
