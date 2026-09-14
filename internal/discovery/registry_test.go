package discovery

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestBackendEntry_Serialization(t *testing.T) {
	entry := BackendEntry{
		ID:     "backend-test-1",
		URL:    "http://192.168.1.100:9001",
		Weight: 5,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal BackendEntry: %v", err)
	}

	var decoded BackendEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal BackendEntry: %v", err)
	}

	if decoded.ID != entry.ID || decoded.URL != entry.URL || decoded.Weight != entry.Weight {
		t.Errorf("decoded entry mismatch: got %+v, want %+v", decoded, entry)
	}
}

func isEtcdAvailable(endpoint string) bool {
	conn, err := net.DialTimeout("tcp", endpoint, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestRegistry_LiveIntegration(t *testing.T) {
	endpoint := "localhost:2379"
	if !isEtcdAvailable(endpoint) {
		t.Skip("skipping etcd integration test: no local etcd server running on", endpoint)
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 1 * time.Second,
	})
	if err != nil {
		t.Skip("cannot connect to etcd:", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reg := NewRegistryFromClient(cli, "/test/proxy/backends/", nil)
	entry := BackendEntry{
		ID:     "live-backend-1",
		URL:    "http://127.0.0.1:9090",
		Weight: 2,
	}

	stopKA, err := reg.Register(ctx, entry, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to register backend: %v", err)
	}
	defer stopKA()

	backends, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("failed to list backends: %v", err)
	}

	found := false
	for _, b := range backends {
		if b.ID == "live-backend-1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("registered backend not found in etcd list: %+v", backends)
	}

	if err := reg.Deregister(ctx, "live-backend-1"); err != nil {
		t.Fatalf("failed to deregister backend: %v", err)
	}
}
