package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	consoleclient "github.com/console-dns/client"
	"github.com/console-dns/spec/models"
)

const maxCleanupDeletes = 32

var providerOperations sync.Mutex

// Client is the small adapter used by the cert-manager solver. It deliberately
// creates an SDK client for each solver configuration, while the operation
// mutex serializes all instances in this process because the supported server
// versions mutate shared in-memory record slices without synchronization.
type Client struct {
	sdk     *consoleclient.ConsoleDnsClient
	initErr error
}

func New(server, token string) *Client {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	c := &Client{}
	if err := validateServer(server); err != nil {
		c.initErr = err
		return c
	}
	if strings.TrimSpace(token) == "" {
		c.initErr = errors.New("provider: API token is empty")
		return c
	}
	c.sdk = consoleclient.NewConsoleDnsClient(server, token)
	return c
}

func (c *Client) EnsureTXT(zone, name, key string, ttl uint32) error {
	if err := c.validateOperation(zone, name, key, ttl); err != nil {
		return err
	}
	providerOperations.Lock()
	defer providerOperations.Unlock()

	state, err := c.read(zone, name)
	if err != nil {
		return err
	}
	if state.has(key) {
		return nil
	}

	if err := c.create(zone, name, key, ttl); err != nil {
		if typed, ok := err.(*providerError); ok && typed.response != nil &&
			(typed.response.StatusCode == http.StatusUnauthorized || typed.response.StatusCode == http.StatusForbidden) {
			return err
		}
		if confirmed, confirmErr := c.confirmPresent(zone, name, key); confirmErr == nil && confirmed {
			return nil
		}
		return err
	}
	confirmed, err := c.confirmPresent(zone, name, key)
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("provider: verify create %s/%s TXT: record is not visible", zone, name)
	}
	return nil
}

func (c *Client) DeleteTXT(zone, name, key string, ttl uint32) error {
	if err := c.validateOperation(zone, name, key, ttl); err != nil {
		return err
	}
	providerOperations.Lock()
	defer providerOperations.Unlock()

	for attempt := 0; attempt < maxCleanupDeletes; attempt++ {
		state, err := c.read(zone, name)
		if err != nil {
			return err
		}
		if record := state.first(key); record != nil {
			if err := c.delete(zone, name, record); err != nil {
				if isKnownMissing(err.upstream, err.response) {
					continue
				}
				return err
			}
			continue
		}

		deleted, err := c.deleteProbe(zone, name, key, ttl)
		if err != nil {
			return err
		}
		if deleted {
			continue
		}
		return nil
	}
	return fmt.Errorf("provider: clean up %s/%s TXT: exceeded %d delete attempts", zone, name, maxCleanupDeletes)
}

type recordState struct {
	zone *models.Zone
	name string
}

func (s recordState) records() []*models.RecordTXT {
	if s.zone == nil || s.zone.Records == nil {
		return nil
	}
	record := s.zone.Records[s.name]
	if record == nil {
		return nil
	}
	return record.TXT
}

func (s recordState) has(key string) bool {
	return s.first(key) != nil
}

func (s recordState) first(key string) *models.RecordTXT {
	for _, record := range s.records() {
		if record != nil && record.Text == key {
			return record
		}
	}
	return nil
}

func (c *Client) read(zone, name string) (recordState, error) {
	result, response, err := c.sdk.ListZone(zone)
	if err != nil {
		return recordState{}, sdkError("list", zone, name, response, err)
	}
	return recordState{zone: result, name: name}, nil
}

func (c *Client) create(zone, name, key string, ttl uint32) error {
	record := &models.RecordTXT{Text: key, Ttl: ttl}
	response, err := c.sdk.CreateRecord(zone, name, "TXT", record)
	if err != nil {
		return sdkError("create", zone, name, response, err)
	}
	return nil
}

func (c *Client) delete(zone, name string, record *models.RecordTXT) *providerError {
	response, err := c.sdk.DeleteRecord(zone, name, "TXT", record)
	if err != nil {
		return &providerError{operation: "delete", zone: zone, name: name, response: response, upstream: err}
	}
	return nil
}

func (c *Client) deleteProbe(zone, name, key string, ttl uint32) (bool, error) {
	err := c.delete(zone, name, &models.RecordTXT{Text: key, Ttl: ttl})
	if err == nil {
		return true, nil
	}
	if isKnownMissing(err.upstream, err.response) {
		return false, nil
	}
	return false, err
}

func (c *Client) confirmPresent(zone, name, key string) (bool, error) {
	state, err := c.read(zone, name)
	if err != nil {
		return false, err
	}
	// Presentation verification is strictly read-only. Deleting a value to
	// probe permissions here could interrupt an active ACME validation.
	return state.has(key), nil
}

type providerError struct {
	operation string
	zone      string
	name      string
	response  *http.Response
	upstream  error
}

func (e *providerError) Error() string {
	if e.response != nil {
		return fmt.Sprintf("provider: %s %s/%s TXT: API returned HTTP %d", e.operation, e.zone, e.name, e.response.StatusCode)
	}
	return fmt.Sprintf("provider: %s %s/%s TXT: %v", e.operation, e.zone, e.name, e.upstream)
}

func sdkError(operation, zone, name string, response *http.Response, err error) error {
	return &providerError{operation: operation, zone: zone, name: name, response: response, upstream: err}
}

func isKnownMissing(err error, response *http.Response) bool {
	if err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
		return false
	}
	// These exact responses are emitted after the Console DNS handler has
	// checked deletion permission. A generic 404 or matching substring is not
	// enough evidence that deletion is authorized and the value is absent.
	switch strings.TrimSpace(err.Error()) {
	case "record not found", "内容删除错误: record not found",
		"当前 TXT 记录不存在", "内容删除错误: 当前 TXT 记录不存在":
		return true
	default:
		return false
	}
}

func (c *Client) validateOperation(zone, name, key string, ttl uint32) error {
	if c == nil {
		return errors.New("provider: client is nil")
	}
	if c.initErr != nil {
		return c.initErr
	}
	if zone == "" || name == "" {
		return errors.New("provider: zone and record name are required")
	}
	if key == "" {
		return errors.New("provider: TXT value is required")
	}
	if ttl == 0 {
		return errors.New("provider: TXT TTL must be positive")
	}
	for label, value := range map[string]string{"zone": zone, "record name": name} {
		if strings.ContainsAny(value, "/\\?#") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("provider: %s contains an invalid path character", label)
		}
	}
	return nil
}

func validateServer(server string) error {
	if server == "" {
		return errors.New("provider: API server is empty")
	}
	u, err := url.Parse(server)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("provider: API server must be an absolute URL without credentials, query, or fragment")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("provider: API server scheme must be http or https")
	}
	return nil
}
