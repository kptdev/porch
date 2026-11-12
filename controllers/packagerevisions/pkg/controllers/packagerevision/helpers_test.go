package packagerevision

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// fakeManager is a minimal ctrl.Manager for unit testing Init().
// Only GetClient() and GetWebhookServer() are implemented; all other methods will panic if called.
type fakeManager struct {
	manager.Manager
	client    client.Client
	runnables []manager.Runnable
	addErr    error
}

func (f *fakeManager) GetClient() client.Client {
	return f.client
}

// Add records the runnable so a test can run it, as the manager would after start.
func (f *fakeManager) Add(r manager.Runnable) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.runnables = append(f.runnables, r)
	return nil
}

// GetAPIReader returns the same client: Init only passes it on to the validator.
func (f *fakeManager) GetAPIReader() client.Reader {
	return f.client
}

func (f *fakeManager) GetWebhookServer() webhook.Server {
	return &fakeWebhookServer{}
}

// fakeWebhookServer is a minimal webhook.Server for testing.
type fakeWebhookServer struct{}

func (f *fakeWebhookServer) Register(path string, handler http.Handler) {
	// No-op for testing
}

func (f *fakeWebhookServer) Start(ctx context.Context) error {
	return nil
}

func (f *fakeWebhookServer) NeedLeaderElection() bool {
	return false
}

func (f *fakeWebhookServer) StartedChecker() healthz.Checker {
	// Return a no-op checker for testing
	return healthz.Ping
}

func (f *fakeWebhookServer) WebhookMux() *http.ServeMux {
	return nil
}
