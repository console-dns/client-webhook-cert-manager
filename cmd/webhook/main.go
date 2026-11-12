package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
	"github.com/console-dns/client-webhook-cert-manager/internal/provider"
	"github.com/console-dns/client-webhook-cert-manager/internal/solver"
	"k8s.io/apimachinery/pkg/util/validation"
)

var version = "dev"

func main() {
	group, options, err := configuration(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if err := provider.ConfigureHTTP(options); err != nil {
		log.Fatalf("configure DNS API transport: %v", err)
	}
	log.Printf("starting Console DNS webhook version=%s group=%s dns-api-insecure-skip-tls-verify=%t", version, group, options.InsecureSkipTLSVerify)
	cmd.RunWebhookServer(group, solver.New())
}

func configuration(getenv func(string) string) (string, provider.HTTPOptions, error) {
	group := getenv("GROUP_NAME")
	if group == "" {
		group = "acme.console-dns.github.com"
	}
	if len(validation.IsDNS1123Subdomain(group)) != 0 {
		return "", provider.HTTPOptions{}, fmt.Errorf("GROUP_NAME must be a valid DNS subdomain")
	}
	opts := provider.HTTPOptions{Timeout: 10 * time.Second, CAFile: getenv("DNS_API_CA_FILE")}
	if value := getenv("DNS_API_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration > time.Minute {
			return "", opts, fmt.Errorf("DNS_API_TIMEOUT must be a duration greater than zero and at most 1m")
		}
		opts.Timeout = duration
	}
	if value := getenv("DNS_API_INSECURE_SKIP_TLS_VERIFY"); value != "" {
		skip, err := strconv.ParseBool(value)
		if err != nil {
			return "", opts, fmt.Errorf("DNS_API_INSECURE_SKIP_TLS_VERIFY must be a boolean")
		}
		opts.InsecureSkipTLSVerify = skip
	}
	return group, opts, nil
}
