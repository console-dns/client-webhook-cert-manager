package solver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type operation struct {
	cleanup         bool
	zone, name, key string
	ttl             uint32
}

type fakeProvider struct {
	operations []operation
	err        error
}

func (p *fakeProvider) EnsureTXT(zone, name, key string, ttl uint32) error {
	p.operations = append(p.operations, operation{false, zone, name, key, ttl})
	return p.err
}

func (p *fakeProvider) DeleteTXT(zone, name, key string, ttl uint32) error {
	p.operations = append(p.operations, operation{true, zone, name, key, ttl})
	return p.err
}

func challenge() *v1alpha1.ChallengeRequest {
	return &v1alpha1.ChallengeRequest{
		Type: "dns-01", DNSName: "*.original.example.org", Key: "challenge-value",
		ResolvedFQDN: "validation.example.com.", ResolvedZone: "example.com.", ResourceNamespace: "issuer-secrets",
		Config: &apiextensionsv1.JSON{Raw: []byte(`{"server":"https://dns.example.com/","tokenSecretRef":{"name":"api-token","key":"token"}}`)},
	}
}

func testSolver() (*Solver, *fake.Clientset, *fakeProvider, *[]string) {
	kube := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-token", Namespace: "issuer-secrets"},
		Data:       map[string][]byte{"token": []byte("first-token\n")},
	})
	p := &fakeProvider{}
	tokens := []string{}
	s := New()
	s.kube = kube
	s.newClient = func(server, token string) txtProvider {
		tokens = append(tokens, token)
		return p
	}
	return s, kube, p, &tokens
}

func TestChallengeLifecycleAndSecretRotation(t *testing.T) {
	s, kube, p, tokens := testSolver()
	ch := challenge()
	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}
	secret, err := kube.CoreV1().Secrets(ch.ResourceNamespace).Get(context.Background(), "api-token", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data["token"] = []byte("rotated-token")
	if _, err := kube.CoreV1().Secrets(ch.ResourceNamespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanUp(ch); err != nil {
		t.Fatal(err)
	}
	if len(*tokens) != 2 || (*tokens)[0] != "first-token" || (*tokens)[1] != "rotated-token" {
		t.Fatalf("token rotation not honored: %v", *tokens)
	}
	want := []operation{{false, "example.com", "validation", "challenge-value", 60}, {true, "example.com", "validation", "challenge-value", 60}}
	if len(p.operations) != len(want) {
		t.Fatalf("unexpected operations: %+v", p.operations)
	}
	for i := range want {
		if p.operations[i] != want[i] {
			t.Errorf("operation %d = %+v, want %+v", i, p.operations[i], want[i])
		}
	}
	for _, action := range kube.Actions() {
		if action.GetNamespace() != "issuer-secrets" {
			t.Errorf("accessed wrong namespace: %s", action.GetNamespace())
		}
	}
}

func TestInvalidChallengesDoNotAccessSecrets(t *testing.T) {
	for name, mutate := range map[string]func(*v1alpha1.ChallengeRequest){
		"type":        func(ch *v1alpha1.ChallengeRequest) { ch.Type = "http-01" },
		"key":         func(ch *v1alpha1.ChallengeRequest) { ch.Key = "" },
		"key control": func(ch *v1alpha1.ChallengeRequest) { ch.Key = "bad\nkey" },
		"namespace":   func(ch *v1alpha1.ChallengeRequest) { ch.ResourceNamespace = "" },
		"zone":        func(ch *v1alpha1.ChallengeRequest) { ch.ResolvedZone = "other.com." },
		"config":      func(ch *v1alpha1.ChallengeRequest) { ch.Config = nil },
	} {
		t.Run(name, func(t *testing.T) {
			s, kube, p, _ := testSolver()
			ch := challenge()
			mutate(ch)
			if err := s.Present(ch); err == nil {
				t.Fatal("accepted invalid challenge")
			}
			if len(kube.Actions()) != 0 || len(p.operations) != 0 {
				t.Fatal("invalid request performed external operations")
			}
		})
	}
	if err := New().Present(nil); err == nil {
		t.Fatal("accepted nil request")
	}
	if err := New().Present(challenge()); err == nil {
		t.Fatal("accepted uninitialized solver")
	}
}

func TestMissingAndInvalidSecret(t *testing.T) {
	for _, token := range []string{"", " \n", "invalid\ntoken", "invalid token"} {
		s, kube, p, _ := testSolver()
		secret, _ := kube.CoreV1().Secrets("issuer-secrets").Get(context.Background(), "api-token", metav1.GetOptions{})
		secret.Data["token"] = []byte(token)
		if _, err := kube.CoreV1().Secrets("issuer-secrets").Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := s.Present(challenge()); err == nil || len(p.operations) != 0 {
			t.Fatalf("invalid token was accepted: %v", err)
		}
	}
	s, kube, p, _ := testSolver()
	if err := kube.CoreV1().Secrets("issuer-secrets").Delete(context.Background(), "api-token", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Present(challenge()); err == nil || !strings.Contains(err.Error(), "issuer-secrets/api-token") || len(p.operations) != 0 {
		t.Fatalf("missing Secret not handled: %v", err)
	}
}

func TestProviderErrorIsReturned(t *testing.T) {
	s, _, p, _ := testSolver()
	p.err = errors.New("provider unavailable")
	if err := s.Present(challenge()); !errors.Is(err, p.err) {
		t.Fatalf("provider error was lost: %v", err)
	}
}
