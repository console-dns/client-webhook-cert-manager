//go:build integration

// The integration build tag keeps the envtest and local DNS conformance suite
// out of the normal unit-test run. Run this suite with the cert-manager
// envtest binaries available through TEST_ASSET_ETCD,
// TEST_ASSET_KUBE_APISERVER, and TEST_ASSET_KUBECTL.
package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook"
	whapi "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	acmedns "github.com/cert-manager/cert-manager/test/acme"
	"github.com/console-dns/client-webhook-cert-manager/internal/provider"
	"github.com/console-dns/client-webhook-cert-manager/internal/solver"
	"github.com/console-dns/spec/models"
	"github.com/miekg/dns"
)

const (
	integrationToken = "integration-token"
	integrationZone  = "example.com"
	integrationName  = "cert-manager-dns01-tests"
	integrationFQDN  = integrationName + "." + integrationZone + "."
)

// TestConsoleDNSConformance runs the cert-manager provider fixture against a
// real envtest API server, the real solver, and the pinned console-dns SDK.
// RunBasic and RunExtended are deliberately called separately because the
// v1.21.3 RunConformance wrapper nests setup and teardown in a way that can
// stop the control plane before the second suite starts.
func TestConsoleDNSConformance(t *testing.T) {
	api := newFakeConsoleDNS()
	apiServer := httptest.NewTLSServer(api)
	defer apiServer.Close()

	dnsServer, err := newPublishingDNSServer(api)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dnsServer.Shutdown(); err != nil {
			t.Errorf("shut down DNS server: %v", err)
		}
	}()

	// The pinned SDK only uses http.DefaultClient. Configure the SDK bridge
	// before the fixture starts, and restore the process default after the
	// complete integration test. InsecureSkipTLSVerify is intentional here:
	// the API uses httptest's self-signed certificate and this exercises the
	// production TLS bypass option without trusting a test CA globally.
	previousHTTPClient := http.DefaultClient
	if err := provider.ConfigureHTTP(provider.HTTPOptions{
		Timeout:               5 * time.Second,
		InsecureSkipTLSVerify: true,
	}); err != nil {
		t.Fatalf("configure console-dns HTTP client: %v", err)
	}
	defer func() { http.DefaultClient = previousHTTPClient }()

	manifestPath := writeFixtureManifests(t, apiServer.URL)
	fixture := acmedns.NewFixture(
		&fixtureSolver{Solver: solver.New()},
		acmedns.SetResolvedFQDN(integrationFQDN),
		acmedns.SetResolvedZone(integrationZone+"."),
		acmedns.SetDNSName(integrationZone),
		acmedns.SetDNSChallengeKey("123d=="),
		acmedns.SetStrict(true),
		acmedns.SetUseAuthoritative(false),
		acmedns.SetDNSServer(dnsServer.Addr()),
		acmedns.SetPollInterval(100*time.Millisecond),
		acmedns.SetPropagationLimit(5*time.Second),
		acmedns.SetManifestPath(manifestPath),
	)

	fixture.RunBasic(t)
	fixture.RunExtended(t)
}

// fixtureSolver adapts the v1.21.3 fixture's request construction. The
// fixture's buildChallengeRequest omits Type even though the production
// solver correctly requires Type == "dns-01". It delegates every operation to
// the real solver after filling only this missing fixture field.
type fixtureSolver struct {
	webhook.Solver
}

var _ webhook.Solver = (*fixtureSolver)(nil)

func (s *fixtureSolver) Present(ch *whapi.ChallengeRequest) error {
	return s.Solver.Present(withDNS01Type(ch))
}

func (s *fixtureSolver) CleanUp(ch *whapi.ChallengeRequest) error {
	return s.Solver.CleanUp(withDNS01Type(ch))
}

func withDNS01Type(ch *whapi.ChallengeRequest) *whapi.ChallengeRequest {
	if ch == nil {
		return nil
	}
	clone := *ch
	clone.Type = "dns-01"
	return &clone
}

func writeFixtureManifests(t *testing.T, serverURL string) string {
	t.Helper()
	dir := t.TempDir()
	config := map[string]any{
		"server": serverURL,
		"tokenSecretRef": map[string]string{
			"name": "console-dns-api-token",
			"key":  "token",
		},
		"ttl": 60,
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal fixture config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), configJSON, 0o600); err != nil {
		t.Fatalf("write fixture config: %v", err)
	}
	secret := `apiVersion: v1
kind: Secret
metadata:
  name: console-dns-api-token
type: Opaque
stringData:
  token: integration-token
`
	if err := os.WriteFile(filepath.Join(dir, "secret.yaml"), []byte(secret), 0o600); err != nil {
		t.Fatalf("write fixture Secret: %v", err)
	}
	return dir
}

type fakeConsoleDNS struct {
	mu      sync.Mutex
	records map[string][]*models.RecordTXT
}

func newFakeConsoleDNS() *fakeConsoleDNS {
	return &fakeConsoleDNS{records: make(map[string][]*models.RecordTXT)}
}

func (f *fakeConsoleDNS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+integrationToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/zones/"), "/")
	if len(parts) < 1 || parts[0] != integrationZone {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		if len(parts) != 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.records) == 0 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
			return
		}
		zone := models.NewZone()
		for name, records := range f.records {
			zone.Records[name] = &models.Record{TXT: cloneTXT(records)}
		}
		writeJSON(w, zone)
		return
	}

	if r.Method != http.MethodPost || len(parts) < 3 || parts[2] != "TXT" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	name := parts[1]
	var record models.RecordTXT
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(parts) == 4 && parts[3] == "delete" {
		for i, existing := range f.records[name] {
			if existing != nil && existing.Text == record.Text {
				f.records[name] = append(f.records[name][:i], f.records[name][i+1:]...)
				if len(f.records[name]) == 0 {
					delete(f.records, name)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("内容删除错误: record not found"))
		return
	}
	if len(parts) != 3 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	recordCopy := record
	f.records[name] = append(f.records[name], &recordCopy)
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func cloneTXT(records []*models.RecordTXT) []*models.RecordTXT {
	clone := make([]*models.RecordTXT, 0, len(records))
	for _, record := range records {
		if record != nil {
			clone = append(clone, record.Clone())
		}
	}
	return clone
}

type publishingDNSServer struct {
	server *dns.Server
	conn   net.PacketConn
	api    *fakeConsoleDNS
}

func newPublishingDNSServer(api *fakeConsoleDNS) (*publishingDNSServer, error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen local DNS server: %w", err)
	}
	result := &publishingDNSServer{conn: conn, api: api}
	result.server = &dns.Server{
		PacketConn: conn,
		Handler:    dns.HandlerFunc(result.serveDNS),
	}
	started := make(chan struct{})
	result.server.NotifyStartedFunc = func() { close(started) }
	go func() {
		if err := result.server.ActivateAndServe(); err != nil {
			// Shutdown closes the packet connection and returns a normal error;
			// the fixture reports query failures if the server exits unexpectedly.
		}
	}()
	<-started
	return result, nil
}

func (s *publishingDNSServer) Addr() string { return s.conn.LocalAddr().String() }

func (s *publishingDNSServer) Shutdown() error { return s.server.Shutdown() }

func (s *publishingDNSServer) serveDNS(w dns.ResponseWriter, request *dns.Msg) {
	response := new(dns.Msg)
	response.SetReply(request)
	response.Authoritative = true
	if len(request.Question) == 0 {
		_ = w.WriteMsg(response)
		return
	}

	question := request.Question[0]
	qname := strings.ToLower(strings.TrimSuffix(question.Name, "."))
	switch question.Qtype {
	case dns.TypeSOA:
		if qname == integrationZone {
			host := integrationZone + "."
			rr, _ := dns.NewRR(fmt.Sprintf("%s 60 IN SOA ns1.%s admin.%s 1 60 60 60 60", host, host, host))
			response.Answer = append(response.Answer, rr)
		}
	case dns.TypeTXT:
		if strings.HasSuffix(qname, "."+integrationZone) {
			name := strings.TrimSuffix(qname, "."+integrationZone)
			s.api.mu.Lock()
			records := cloneTXT(s.api.records[name])
			s.api.mu.Unlock()
			for _, record := range records {
				response.Answer = append(response.Answer, &dns.TXT{
					Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: record.Ttl},
					Txt: []string{record.Text},
				})
			}
		}
	}
	_ = w.WriteMsg(response)
}
