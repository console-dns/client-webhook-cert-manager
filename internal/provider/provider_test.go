package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/console-dns/spec/models"
)

type fakeDNS struct {
	mu                 sync.Mutex
	records            map[string][]*models.RecordTXT
	createCount        int
	deleteCount        int
	deleteTTLs         []uint32
	listNull           bool
	listStatus         int
	deleteStatus       int
	unknownDeleteError bool
	ignoreDeletes      bool
}

func newFakeDNS() *fakeDNS {
	return &fakeDNS{records: make(map[string][]*models.RecordTXT)}
}

func (f *fakeDNS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/zones/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodGet {
		if f.listStatus != 0 {
			w.WriteHeader(f.listStatus)
			return
		}
		if f.listNull {
			_, _ = w.Write([]byte("null"))
			return
		}
		zone := &models.Zone{Records: map[string]*models.Record{}}
		prefix := parts[0] + "/"
		for key, values := range f.records {
			if strings.HasPrefix(key, prefix) && len(values) > 0 {
				name := strings.TrimPrefix(key, prefix)
				zone.Records[name] = &models.Record{TXT: cloneTXT(values)}
			}
		}
		_ = json.NewEncoder(w).Encode(zone)
		return
	}
	if r.Method != http.MethodPost || len(parts) < 3 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key := parts[0] + "/" + parts[1]
	var record models.RecordTXT
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	isDelete := len(parts) >= 4 && parts[3] == "delete"
	if isDelete {
		f.deleteCount++
		f.deleteTTLs = append(f.deleteTTLs, record.Ttl)
		if f.deleteStatus != 0 {
			w.WriteHeader(f.deleteStatus)
			return
		}
		if f.unknownDeleteError {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("arbitrary invalid request"))
			return
		}
		for i, existing := range f.records[key] {
			if existing.Text == record.Text {
				if !f.ignoreDeletes {
					f.records[key] = append(f.records[key][:i], f.records[key][i+1:]...)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("内容删除错误: record not found"))
		return
	}
	f.createCount++
	f.records[key] = append(f.records[key], &record)
	w.WriteHeader(http.StatusOK)
}

func cloneTXT(values []*models.RecordTXT) []*models.RecordTXT {
	result := make([]*models.RecordTXT, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, value.Clone())
		}
	}
	return result
}

func fakeClient(t *testing.T, fake *fakeDNS) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(fake)
	return New(server.URL, "test-token"), server.Close
}

func TestEnsureTXTIsIdempotentAndPreservesValues(t *testing.T) {
	fake := newFakeDNS()
	fake.records["example.com/_acme-challenge"] = []*models.RecordTXT{
		{Text: "other", Ttl: 300},
		{Text: "challenge", Ttl: 30},
		{Text: "challenge", Ttl: 60},
	}
	client, closeServer := fakeClient(t, fake)
	defer closeServer()
	if err := client.EnsureTXT("example.com", "_acme-challenge", "challenge", 120); err != nil {
		t.Fatal(err)
	}
	if fake.createCount != 0 {
		t.Fatalf("idempotent ensure created %d records", fake.createCount)
	}
	if err := client.DeleteTXT("example.com", "_acme-challenge", "challenge", 120); err != nil {
		t.Fatal(err)
	}
	if len(fake.records["example.com/_acme-challenge"]) != 1 || fake.records["example.com/_acme-challenge"][0].Text != "other" {
		t.Fatalf("cleanup did not preserve other TXT values: %#v", fake.records)
	}
	if len(fake.deleteTTLs) < 2 || fake.deleteTTLs[0] != 30 || fake.deleteTTLs[1] != 60 {
		t.Fatalf("cleanup did not use stored TTLs: %v", fake.deleteTTLs)
	}
}

func TestEnsureTXTCreatesOnlyOnce(t *testing.T) {
	fake := newFakeDNS()
	client, closeServer := fakeClient(t, fake)
	defer closeServer()
	if err := client.EnsureTXT("example.com", "_acme-challenge", "challenge", 60); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureTXT("example.com", "_acme-challenge", "challenge", 120); err != nil {
		t.Fatal(err)
	}
	if fake.createCount != 1 {
		t.Fatalf("expected one create, got %d", fake.createCount)
	}
}

func TestEnsureTXTRecoversAnUncertainCreate(t *testing.T) {
	fake := newFakeDNS()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/TXT") {
			var record models.RecordTXT
			_ = json.NewDecoder(r.Body).Decode(&record)
			fake.mu.Lock()
			fake.records["example.com/_acme-challenge"] = []*models.RecordTXT{&record}
			fake.mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream failed after commit"))
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := New(server.URL, "test-token")
	if err := client.EnsureTXT("example.com", "_acme-challenge", "challenge", 60); err != nil {
		t.Fatalf("uncertain create was not recovered: %v", err)
	}
}

func TestUnknownDeleteErrorIsNotTreatedAsMissing(t *testing.T) {
	fake := newFakeDNS()
	fake.unknownDeleteError = true
	client, closeServer := fakeClient(t, fake)
	defer closeServer()
	err := client.DeleteTXT("example.com", "_acme-challenge", "challenge", 60)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected unknown HTTP 400, got %v", err)
	}
}

func TestFilteredEmptyReadStillChecksDeletePermission(t *testing.T) {
	fake := newFakeDNS()
	fake.listNull = true
	fake.deleteStatus = http.StatusForbidden
	client, closeServer := fakeClient(t, fake)
	defer closeServer()
	err := client.DeleteTXT("example.com", "_acme-challenge", "challenge", 60)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected permission error, got %v", err)
	}
}

func TestPresentDoesNotDeleteAnUnobservableRecord(t *testing.T) {
	fake := newFakeDNS()
	fake.listNull = true
	client, closeServer := fakeClient(t, fake)
	defer closeServer()
	if err := client.EnsureTXT("example.com", "_acme-challenge", "challenge", 60); err == nil {
		t.Fatal("expected an error when the written record cannot be observed")
	}
	if fake.deleteCount != 0 {
		t.Fatalf("presentation must never delete records, got %d deletes", fake.deleteCount)
	}
}

func TestMissingResponseClassificationIsExact(t *testing.T) {
	for _, tt := range []struct {
		status  int
		message string
		missing bool
	}{
		{400, "内容删除错误: record not found\n", true},
		{400, "当前 TXT 记录不存在", true},
		{403, "record not found", false},
		{404, "record not found", false},
		{400, "backend failed: record not found in permission database", false},
		{400, "区域不存在", false},
	} {
		if got := isKnownMissing(errors.New(tt.message), &http.Response{StatusCode: tt.status}); got != tt.missing {
			t.Errorf("status=%d message=%q: got %t, want %t", tt.status, tt.message, got, tt.missing)
		}
	}
}

func TestCleanupHasABoundedDeleteBudget(t *testing.T) {
	fake := newFakeDNS()
	fake.records["example.com/_acme-challenge"] = []*models.RecordTXT{{Text: "challenge", Ttl: 60}}
	fake.ignoreDeletes = true
	client, closeServer := fakeClient(t, fake)
	defer closeServer()
	err := client.DeleteTXT("example.com", "_acme-challenge", "challenge", 60)
	if err == nil || !strings.Contains(err.Error(), "32 delete attempts") {
		t.Fatalf("expected bounded cleanup error, got %v", err)
	}
	if fake.deleteCount != maxCleanupDeletes {
		t.Fatalf("expected %d deletes, got %d", maxCleanupDeletes, fake.deleteCount)
	}
}

func TestClientValidationDoesNotExposeToken(t *testing.T) {
	client := New("https://dns.example.invalid?token=private-token", "private-token")
	err := client.EnsureTXT("example.com", "_acme-challenge", "key", 60)
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestConcurrentChallengesPreserveOtherValues(t *testing.T) {
	fake := newFakeDNS()
	fake.records["example.com/_acme-challenge"] = []*models.RecordTXT{{Text: "unrelated", Ttl: 300}}
	server := httptest.NewServer(fake)
	defer server.Close()
	const count = 16
	run := func(cleanup bool) {
		t.Helper()
		var work sync.WaitGroup
		errors := make(chan error, count)
		for i := 0; i < count; i++ {
			work.Add(1)
			go func(index int) {
				defer work.Done()
				client := New(server.URL, "test-token")
				key := fmt.Sprintf("challenge-%d", index)
				var err error
				if cleanup {
					err = client.DeleteTXT("example.com", "_acme-challenge", key, 60)
				} else {
					err = client.EnsureTXT("example.com", "_acme-challenge", key, 60)
				}
				errors <- err
			}(i)
		}
		work.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	run(false)
	if got := len(fake.records["example.com/_acme-challenge"]); got != count+1 {
		t.Fatalf("concurrent Present lost records: got %d, want %d", got, count+1)
	}
	run(true)
	records := fake.records["example.com/_acme-challenge"]
	if len(records) != 1 || records[0].Text != "unrelated" {
		t.Fatalf("concurrent cleanup changed unrelated values: %+v", records)
	}
}
