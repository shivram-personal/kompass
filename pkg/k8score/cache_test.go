package k8score

import (
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

// TestNewResourceCache_DeferredSync_PartialFailure verifies that a permanently
// failing deferred informer (e.g. HPA autoscaling/v2 on a K8s <1.23 cluster,
// which responds with "the server could not find the requested resource")
// does not block sibling deferred informers from becoming ready. It also
// verifies the DeferredSyncTimeout path flips deferredFailed so stragglers
// return false from IsDeferredPending.
func TestNewResourceCache_DeferredSync_PartialFailure(t *testing.T) {
	client := fake.NewSimpleClientset()
	// Make HPA LIST fail forever, as happens when the v2 API isn't served.
	client.PrependReactor("list", "horizontalpodautoscalers", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("the server could not find the requested resource")
	})

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods:                     true,
			ConfigMaps:               true,
			Secrets:                  true,
			HorizontalPodAutoscalers: true,
		},
		DeferredTypes: map[string]bool{
			ConfigMaps:               true,
			Secrets:                  true,
			HorizontalPodAutoscalers: true,
		},
		DeferredSyncTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	// Poll for ConfigMaps and Secrets to become ready. A failing sibling
	// (HPA) must not block them.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rc.ConfigMaps() != nil && rc.Secrets() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rc.ConfigMaps() == nil {
		t.Fatal("expected ConfigMaps() to become ready despite sibling HPA failing")
	}
	if rc.Secrets() == nil {
		t.Fatal("expected Secrets() to become ready despite sibling HPA failing")
	}

	// Pre-timeout contract check: while HPA is still stuck and the deadline
	// hasn't fired, IsDeferredPending must report HPA pending (HTTP handlers
	// return 503) and ConfigMaps not-pending (handlers serve data). This is
	// the 503-vs-403 distinction the fix is built around.
	if !rc.IsDeferredPending(HorizontalPodAutoscalers) {
		t.Error("pre-timeout: expected IsDeferredPending(HPA)=true while informer still stuck")
	}
	if rc.IsDeferredPending(ConfigMaps) {
		t.Error("pre-timeout: ConfigMaps synced, expected IsDeferredPending=false")
	}

	// deferredDone must close even though HPA never syncs — otherwise the
	// SSE warmup completion never fires.
	select {
	case <-rc.DeferredDone():
	case <-time.After(3 * time.Second):
		t.Fatal("deferredDone never closed after DeferredSyncTimeout")
	}

	// Post-timeout: HPA flips from pending to not-pending because
	// deferredFailed is now set — stops the perpetual-503 spinner.
	// ConfigMaps stays not-pending (it was already synced).
	if rc.IsDeferredPending(HorizontalPodAutoscalers) {
		t.Error("post-timeout: expected IsDeferredPending(HPA)=false (deferredFailed signals give-up)")
	}
	if rc.IsDeferredPending(ConfigMaps) {
		t.Error("post-timeout: ConfigMaps synced, expected IsDeferredPending=false")
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

func TestNewResourceCache_OnReceived(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "test-uid",
		},
	}
	client := fake.NewSimpleClientset(pod)

	var mu sync.Mutex
	receivedKinds := map[string]int{}

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods: true,
		},
		OnReceived: func(kind string) {
			mu.Lock()
			receivedKinds[kind]++
			mu.Unlock()
		},
		// Even with noisy filter that always returns true, OnReceived should fire
		IsNoisyResource: func(kind, name, op string) bool {
			return true // everything is noisy
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	podCount := receivedKinds["Pod"]
	mu.Unlock()

	if podCount == 0 {
		t.Error("expected OnReceived to fire even when IsNoisyResource returns true")
	}
}

func TestNewResourceCache_NamespaceScopedValidation(t *testing.T) {
	client := fake.NewSimpleClientset()

	_, err := NewResourceCache(CacheConfig{
		Client:          client,
		NamespaceScoped: true,
		Namespace:       "", // empty namespace with NamespaceScoped=true
		ResourceTypes:   map[string]bool{Pods: true},
	})
	if err == nil {
		t.Fatal("expected error when NamespaceScoped=true with empty Namespace")
	}
}

func TestNewResourceCache_CallbackPanicRecovery(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "panic-pod",
			Namespace: "default",
			UID:       "panic-uid",
		},
	}
	client := fake.NewSimpleClientset(pod)

	rc, err := NewResourceCache(CacheConfig{
		Client: client,
		ResourceTypes: map[string]bool{
			Pods: true,
		},
		OnChange: func(change ResourceChange, obj, oldObj any) {
			panic("test panic in OnChange")
		},
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	// Wait for event to fire — the panic should be recovered, not crash
	time.Sleep(200 * time.Millisecond)

	// If we get here without crashing, the test passes
	if rc.Pods() == nil {
		t.Error("expected Pods() lister to still work after callback panic")
	}
}

func TestNewResourceCache_MapCloning(t *testing.T) {
	client := fake.NewSimpleClientset()

	resourceTypes := map[string]bool{
		Pods:     true,
		Services: true,
	}

	rc, err := NewResourceCache(CacheConfig{
		Client:        client,
		ResourceTypes: resourceTypes,
	})
	if err != nil {
		t.Fatalf("NewResourceCache failed: %v", err)
	}
	defer rc.Stop()

	// Mutate the original map after construction
	resourceTypes[Pods] = false
	resourceTypes["bogus"] = true

	// The cache should not be affected
	if rc.Pods() == nil {
		t.Error("expected Pods() lister to still work after caller mutates resourceTypes map")
	}
	enabled := rc.GetEnabledResources()
	if !enabled[Pods] {
		t.Error("expected Pods to still be enabled after caller mutates resourceTypes map")
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
