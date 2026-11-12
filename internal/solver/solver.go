package solver

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"github.com/console-dns/client-webhook-cert-manager/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const Name = "console-dns"

type txtProvider interface {
	EnsureTXT(zone, name, key string, ttl uint32) error
	DeleteTXT(zone, name, key string, ttl uint32) error
}

type Solver struct {
	kube      kubernetes.Interface
	ctx       context.Context
	newClient func(server, token string) txtProvider
}

var _ webhook.Solver = (*Solver)(nil)

func New() *Solver {
	return &Solver{
		ctx: context.Background(),
		newClient: func(server, token string) txtProvider {
			return provider.New(server, token)
		},
	}
}

func (s *Solver) Name() string { return Name }

func (s *Solver) Initialize(kubeConfig *rest.Config, stopCh <-chan struct{}) error {
	if kubeConfig == nil {
		return fmt.Errorf("Kubernetes client configuration is required")
	}
	client, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("initialize Kubernetes client: %w", err)
	}
	s.kube = client
	s.ctx = context.Background()
	if stopCh != nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.ctx = ctx
		go func() {
			<-stopCh
			cancel()
		}()
	}
	return nil
}

func (s *Solver) Present(ch *v1alpha1.ChallengeRequest) error { return s.solve(ch, false) }

func (s *Solver) CleanUp(ch *v1alpha1.ChallengeRequest) error { return s.solve(ch, true) }

func (s *Solver) solve(ch *v1alpha1.ChallengeRequest, cleanup bool) error {
	if ch == nil || ch.Type != "dns-01" {
		return fmt.Errorf("a dns-01 challenge request is required")
	}
	if ch.Key == "" || len(ch.Key) > 512 || strings.IndexFunc(ch.Key, unicode.IsControl) >= 0 {
		return fmt.Errorf("challenge key is empty or invalid")
	}
	if ch.ResourceNamespace == "" || len(validation.IsDNS1123Label(ch.ResourceNamespace)) != 0 {
		return fmt.Errorf("resourceNamespace must be a valid namespace")
	}
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}
	zone, name, err := recordLocation(ch.ResolvedFQDN, ch.ResolvedZone)
	if err != nil {
		return err
	}
	if s.kube == nil {
		return fmt.Errorf("Kubernetes client is not initialized")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	secret, err := s.kube.CoreV1().Secrets(ch.ResourceNamespace).Get(ctx, cfg.TokenSecretRef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read token Secret %s/%s: %w", ch.ResourceNamespace, cfg.TokenSecretRef.Name, err)
	}
	token := strings.TrimSpace(string(secret.Data[cfg.TokenSecretRef.Key]))
	if token == "" || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("token Secret %s/%s key %s is missing, empty, or invalid", ch.ResourceNamespace, cfg.TokenSecretRef.Name, cfg.TokenSecretRef.Key)
	}
	client := s.newClient(cfg.Server, token)
	if cleanup {
		err = client.DeleteTXT(zone, name, ch.Key, *cfg.TTL)
	} else {
		err = client.EnsureTXT(zone, name, ch.Key, *cfg.TTL)
	}
	if err != nil {
		return err
	}
	klog.V(2).InfoS("Processed DNS01 challenge", "cleanup", cleanup, "zone", zone, "record", name, "namespace", ch.ResourceNamespace)
	return nil
}
