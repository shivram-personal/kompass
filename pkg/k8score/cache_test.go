package k8score

import (
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewResourceCache_Basic(t *testing.T) {
	client := fake.NewSimpleClientset()

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods:        true,
			Services:    true,
			Deployments: true,
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	if !rc.IsSyncComplete() {
		t.Error("expected IsSyncComplete() = true after NewResourceCache returns")
	}

	if rc.Pods() == nil {
		t.Error("expected Pods() lister to be non-nil")
	}
	if rc.Services() == nil {
		t.Error("expected Services() lister to be non-nil")
	}
	if rc.Deployments() == nil {
		t.Error("expected Deployments() lister to be non-nil")
	}

	// Disabled resources should return nil listers
	if rc.Secrets() != nil {
		t.Error("expected Secrets() lister to be nil (not enabled)")
	}
	if rc.Nodes() != nil {
		t.Error("expected Nodes() lister to be nil (not enabled)")
	}
}

func TestNewResourceCache_DeferredSync(t *testing.T) {
	client := fake.NewSimpleClientset()

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods:       true,
			Services:   true,
			ConfigMaps: true,
			Secrets:    true,
		},
		DeferredTypes: map[string]bool{
			ConfigMaps: true,
			Secrets:    true,
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	// Critical resources should be available immediately
	if rc.Pods() == nil {
		t.Error("expected Pods() lister to be available (critical)")
	}

	// Wait for deferred to complete (fake client syncs immediately)
	select {
	case <-rc.DeferredDone():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for deferred sync")
	}

	if !rc.IsDeferredSynced() {
		t.Error("expected IsDeferredSynced() = true")
	}

	if rc.ConfigMaps() == nil {
		t.Error("expected ConfigMaps() lister to be available after deferred sync")
	}
	if rc.Secrets() == nil {
		t.Error("expected Secrets() lister to be available after deferred sync")
	}
}

func TestNewResourceCache_Callbacks(t *testing.T) {
	// Pre-create a pod so the informer fires an add event
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "test-uid",
		},
	}
	client := fake.NewSimpleClientset(pod)

	var mu sync.Mutex
	var changes []ResourceChange

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods: true,
		},
		OnChange: func(change ResourceChange, obj, oldObj any) {
			mu.Lock()
			changes = append(changes, change)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	// Wait for the informer add event to propagate
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := len(changes)
	mu.Unlock()

	if got == 0 {
		t.Error("expected OnChange to be called for the pre-existing pod add")
	}
}

func TestNewResourceCache_SuppressInitialAdds(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-pod",
			Namespace: "default",
			UID:       "uid-1",
		},
	}
	client := fake.NewSimpleClientset(pod)

	var mu sync.Mutex
	var callbackChanges []ResourceChange

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods: true,
		},
		SuppressInitialAdds: true,
		OnChange: func(change ResourceChange, obj, oldObj any) {
			mu.Lock()
			callbackChanges = append(callbackChanges, change)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	// Wait briefly for any events
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := len(callbackChanges)
	mu.Unlock()

	// With SuppressInitialAdds, the OnChange callback should NOT fire for
	// pre-existing resources during sync. However, the add events still
	// go to the changes channel. Note: since NewResourceCache blocks until
	// sync completes, the add fires DURING construction when syncComplete
	// is still false, so callback should be suppressed.
	if got != 0 {
		t.Errorf("expected 0 callback changes with SuppressInitialAdds, got %d", got)
	}
}

func TestNewResourceCache_NilClient(t *testing.T) {
	_, err := NewResourceCache(CacheConfig{
		Client: nil,
	})
	if err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestNewResourceCache_NoEnabledResources(t *testing.T) {
	client := fake.NewSimpleClientset()

	rc, err := NewResourceCache(CacheConfig{
		Client:        client,
		ResourceTypes: map[string]bool{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rc.Stop()

	if !rc.IsSyncComplete() {
		t.Error("expected IsSyncComplete() = true even with no resources")
	}
	if rc.GetResourceCount() != 0 {
		t.Errorf("expected 0 resource count, got %d", rc.GetResourceCount())
	}
}

func TestNewResourceCache_StopLifecycle(t *testing.T) {
	client := fake.NewSimpleClientset()

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods: true,
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}

	// Stop should be safe to call multiple times
	rc.Stop()
	rc.Stop()

	// Methods should be safe to call after stop
	_ = rc.Pods()
	_ = rc.Changes()
	_ = rc.GetResourceCount()
}

func TestDropManagedFields(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "test-manager"},
			},
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{"big":"json"}`,
				"keep-this": "yes",
			},
		},
	}

	result, err := DropManagedFields(pod)
	if err != nil {
		t.Fatalf("DropManagedFields failed: %v", err)
	}

	p := result.(*corev1.Pod)
	if len(p.ManagedFields) != 0 {
		t.Error("expected managedFields to be nil/empty")
	}
	if _, ok := p.Annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Error("expected last-applied-configuration annotation to be removed")
	}
	if p.Annotations["keep-this"] != "yes" {
		t.Error("expected other annotations to be preserved")
	}
}

func TestDropManagedFields_Event(t *testing.T) {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-event",
			Namespace: "default",
			UID:       "event-uid",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "test-manager"},
			},
			Labels: map[string]string{"should": "be-stripped"},
		},
		Reason:  "Created",
		Message: "Pod created",
		Type:    "Normal",
		Count:   1,
	}

	result, err := DropManagedFields(event)
	if err != nil {
		t.Fatalf("DropManagedFields failed: %v", err)
	}

	e := result.(*corev1.Event)
	if e.Reason != "Created" {
		t.Errorf("expected Reason=Created, got %s", e.Reason)
	}
	if e.Labels != nil {
		t.Error("expected Labels to be stripped from event")
	}
	if len(e.ManagedFields) != 0 {
		t.Error("expected managedFields to be stripped from event")
	}
}
