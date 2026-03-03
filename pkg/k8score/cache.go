package k8score

import (
	"fmt"
	"log"
	"maps"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// ResourceCache provides fast, eventually-consistent access to K8s resources
// using SharedInformers. It is the shared core used by both Radar and
// skyhook-connector.
type ResourceCache struct {
	factory          informers.SharedInformerFactory
	changes          chan ResourceChange
	stopCh           chan struct{}
	stopOnce         sync.Once
	enabledResources map[string]bool
	deferredSynced   map[string]bool
	deferredMu       sync.RWMutex
	deferredDone     chan struct{}
	syncComplete     bool
	config           CacheConfig
}

type informerSetup struct {
	key     string
	kind    string
	setup   func() cache.SharedIndexInformer
	isEvent bool
}

// NewResourceCache creates and starts a ResourceCache from the given config.
// It blocks until critical (non-deferred) informers have synced, then returns.
// Deferred informers sync in the background.
func NewResourceCache(cfg CacheConfig) (*ResourceCache, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("CacheConfig.Client must not be nil")
	}

	channelSize := cfg.ChannelSize
	if channelSize <= 0 {
		channelSize = 10000
	}

	logf := cfg.TimingLogger
	if logf == nil {
		logf = func(string, ...any) {} // no-op
	}

	stdlog := cfg.Logger
	if stdlog == nil {
		stdlog = log.Default()
	}

	stopCh := make(chan struct{})
	changes := make(chan ResourceChange, channelSize)

	// Build factory options
	factoryOpts := []informers.SharedInformerOption{
		informers.WithTransform(DropManagedFields),
	}
	if cfg.NamespaceScoped && cfg.Namespace != "" {
		factoryOpts = append(factoryOpts, informers.WithNamespace(cfg.Namespace))
		stdlog.Printf("Using namespace-scoped informers for namespace %q", cfg.Namespace)
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		cfg.Client,
		0, // no resync — updates come via watch
		factoryOpts...,
	)

	// Table-driven informer setup — only create informers for enabled types
	setups := buildInformerSetups(factory)

	enabled := cfg.ResourceTypes
	deferredTypes := cfg.DeferredTypes
	if deferredTypes == nil {
		deferredTypes = map[string]bool{}
	}

	var criticalSyncFuncs []cache.InformerSynced
	var deferredSyncFuncs []cache.InformerSynced
	var deferredKeys []string
	enabledCount := 0

	rc := &ResourceCache{
		factory:          factory,
		changes:          changes,
		stopCh:           stopCh,
		enabledResources: enabled,
		config:           cfg,
	}

	for _, s := range setups {
		if !enabled[s.key] {
			continue
		}
		enabledCount++
		inf := s.setup()

		var err error
		if s.isEvent {
			err = rc.addEventHandlers(inf, changes)
		} else {
			err = rc.addChangeHandlers(inf, s.kind, changes)
		}
		if err != nil {
			close(stopCh)
			return nil, fmt.Errorf("failed to register %s event handler: %w", s.kind, err)
		}

		if deferredTypes[s.key] {
			deferredSyncFuncs = append(deferredSyncFuncs, inf.HasSynced)
			deferredKeys = append(deferredKeys, s.key)
		} else {
			criticalSyncFuncs = append(criticalSyncFuncs, inf.HasSynced)
		}
	}

	if enabledCount == 0 {
		stdlog.Printf("Warning: No resource types are accessible (all RBAC checks failed)")
		rc.deferredSynced = make(map[string]bool)
		rc.deferredDone = make(chan struct{})
		close(rc.deferredDone)
		rc.syncComplete = true
		return rc, nil
	}

	// Start all informers
	factory.Start(stopCh)

	stdlog.Printf("Starting resource cache: %d critical + %d deferred informers (%d total)",
		len(criticalSyncFuncs), len(deferredSyncFuncs), enabledCount)
	syncStart := time.Now()

	// Track per-informer sync times
	for _, s := range setups {
		if !enabled[s.key] {
			continue
		}
		kind := s.kind
		key := s.key
		isDeferred := deferredTypes[key]
		var fn cache.InformerSynced
		if isDeferred {
			for i, dk := range deferredKeys {
				if dk == key {
					fn = deferredSyncFuncs[i]
					break
				}
			}
		} else {
			idx := 0
			for _, ss := range setups {
				if !enabled[ss.key] || deferredTypes[ss.key] {
					continue
				}
				if ss.key == key {
					fn = criticalSyncFuncs[idx]
					break
				}
				idx++
			}
		}
		if fn != nil {
			tag := "critical"
			if isDeferred {
				tag = "deferred"
			}
			go func() {
				t := time.Now()
				for !fn() {
					select {
					case <-stopCh:
						return
					default:
					}
					time.Sleep(10 * time.Millisecond)
				}
				logf("    Informer synced: %-28s %v (%s)", kind, time.Since(t), tag)
			}()
		}
	}

	// Phase 1: Wait for critical informers
	if len(criticalSyncFuncs) > 0 {
		if !cache.WaitForCacheSync(stopCh, criticalSyncFuncs...) {
			close(stopCh)
			return nil, fmt.Errorf("failed to sync critical resource caches")
		}
	}
	logf("    Phase 1 sync (%d critical informers): %v", len(criticalSyncFuncs), time.Since(syncStart))
	stdlog.Printf("Critical resource caches synced in %v — UI can render", time.Since(syncStart))

	rc.syncComplete = true

	// Build deferred tracking state
	deferredSynced := make(map[string]bool, len(deferredKeys))
	for _, k := range deferredKeys {
		deferredSynced[k] = false
	}
	deferredDone := make(chan struct{})
	rc.deferredSynced = deferredSynced
	rc.deferredDone = deferredDone

	// Phase 2: Wait for deferred informers in background
	if len(deferredSyncFuncs) > 0 {
		go func() {
			deferredStart := time.Now()
			if cache.WaitForCacheSync(stopCh, deferredSyncFuncs...) {
				rc.deferredMu.Lock()
				for _, k := range deferredKeys {
					rc.deferredSynced[k] = true
				}
				rc.deferredMu.Unlock()
				close(deferredDone)
				logf("    Phase 2 sync (%d deferred informers): %v", len(deferredSyncFuncs), time.Since(deferredStart))
				stdlog.Printf("Deferred resource caches synced in %v (total: %v)", time.Since(deferredStart), time.Since(syncStart))
			} else {
				stdlog.Printf("ERROR: Deferred resource cache sync failed after %v", time.Since(deferredStart))
				close(deferredDone)
			}
		}()
	} else {
		close(deferredDone)
	}

	return rc, nil
}

// buildInformerSetups returns the table-driven informer setup list.
func buildInformerSetups(factory informers.SharedInformerFactory) []informerSetup {
	return []informerSetup{
		{Services, "Service", func() cache.SharedIndexInformer { return factory.Core().V1().Services().Informer() }, false},
		{Pods, "Pod", func() cache.SharedIndexInformer { return factory.Core().V1().Pods().Informer() }, false},
		{Nodes, "Node", func() cache.SharedIndexInformer { return factory.Core().V1().Nodes().Informer() }, false},
		{Namespaces, "Namespace", func() cache.SharedIndexInformer { return factory.Core().V1().Namespaces().Informer() }, false},
		{ConfigMaps, "ConfigMap", func() cache.SharedIndexInformer { return factory.Core().V1().ConfigMaps().Informer() }, false},
		{Secrets, "Secret", func() cache.SharedIndexInformer { return factory.Core().V1().Secrets().Informer() }, false},
		{Events, "Event", func() cache.SharedIndexInformer { return factory.Core().V1().Events().Informer() }, true},
		{PersistentVolumeClaims, "PersistentVolumeClaim", func() cache.SharedIndexInformer { return factory.Core().V1().PersistentVolumeClaims().Informer() }, false},
		{PersistentVolumes, "PersistentVolume", func() cache.SharedIndexInformer { return factory.Core().V1().PersistentVolumes().Informer() }, false},
		{Deployments, "Deployment", func() cache.SharedIndexInformer { return factory.Apps().V1().Deployments().Informer() }, false},
		{DaemonSets, "DaemonSet", func() cache.SharedIndexInformer { return factory.Apps().V1().DaemonSets().Informer() }, false},
		{StatefulSets, "StatefulSet", func() cache.SharedIndexInformer { return factory.Apps().V1().StatefulSets().Informer() }, false},
		{ReplicaSets, "ReplicaSet", func() cache.SharedIndexInformer { return factory.Apps().V1().ReplicaSets().Informer() }, false},
		{Ingresses, "Ingress", func() cache.SharedIndexInformer { return factory.Networking().V1().Ingresses().Informer() }, false},
		{IngressClasses, "IngressClass", func() cache.SharedIndexInformer { return factory.Networking().V1().IngressClasses().Informer() }, false},
		{Jobs, "Job", func() cache.SharedIndexInformer { return factory.Batch().V1().Jobs().Informer() }, false},
		{CronJobs, "CronJob", func() cache.SharedIndexInformer { return factory.Batch().V1().CronJobs().Informer() }, false},
		{HorizontalPodAutoscalers, "HorizontalPodAutoscaler", func() cache.SharedIndexInformer {
			return factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer()
		}, false},
		{StorageClasses, "StorageClass", func() cache.SharedIndexInformer { return factory.Storage().V1().StorageClasses().Informer() }, false},
		{PodDisruptionBudgets, "PodDisruptionBudget", func() cache.SharedIndexInformer { return factory.Policy().V1().PodDisruptionBudgets().Informer() }, false},
		{ServiceAccounts, "ServiceAccount", func() cache.SharedIndexInformer { return factory.Core().V1().ServiceAccounts().Informer() }, false},
	}
}

// addChangeHandlers registers event handlers for non-Event resource changes.
func (rc *ResourceCache) addChangeHandlers(inf cache.SharedIndexInformer, kind string, ch chan<- ResourceChange) error {
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			rc.enqueueChange(ch, kind, obj, nil, "add")
		},
		UpdateFunc: func(oldObj, newObj any) {
			rc.enqueueChange(ch, kind, newObj, oldObj, "update")
		},
		DeleteFunc: func(obj any) {
			rc.enqueueChange(ch, kind, obj, nil, "delete")
		},
	})
	return err
}

// addEventHandlers registers special handlers for K8s Events.
// Events use a separate path: no noisy filtering, no diff computation.
func (rc *ResourceCache) addEventHandlers(inf cache.SharedIndexInformer, ch chan<- ResourceChange) error {
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			rc.enqueueEvent(ch, obj, "add")
		},
		UpdateFunc: func(oldObj, newObj any) {
			rc.enqueueEvent(ch, newObj, "update")
		},
		DeleteFunc: func(obj any) {
			rc.enqueueEvent(ch, obj, "delete")
		},
	})
	return err
}

// enqueueChange handles non-Event resource change notifications.
func (rc *ResourceCache) enqueueChange(ch chan<- ResourceChange, kind string, obj any, oldObj any, op string) {
	meta, ok := obj.(metav1.Object)
	if !ok {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			meta, ok = tombstone.Obj.(metav1.Object)
			if !ok {
				return
			}
			obj = tombstone.Obj
		} else {
			return
		}
	}

	ns := meta.GetNamespace()
	name := meta.GetName()
	uid := string(meta.GetUID())

	// Check if noisy (skip OnChange but still send to channel)
	skipCallback := false
	if rc.config.IsNoisyResource != nil && rc.config.IsNoisyResource(kind, name, op) {
		skipCallback = true
		if rc.config.OnDrop != nil {
			rc.config.OnDrop(kind, ns, name, "noisy_filter", op)
		}
	}

	// SuppressInitialAdds: during initial sync, skip OnChange for adds
	if op == "add" && rc.config.SuppressInitialAdds && !rc.syncComplete {
		skipCallback = true
	}

	// Compute diff for updates
	var diff *DiffInfo
	if op == "update" && oldObj != nil && obj != nil && rc.config.ComputeDiff != nil {
		diff = rc.config.ComputeDiff(kind, oldObj, obj)
	}

	// Fire OnChange callback (before channel send, matching existing behavior)
	if !skipCallback && rc.config.OnChange != nil {
		change := ResourceChange{
			Kind:      kind,
			Namespace: ns,
			Name:      name,
			UID:       uid,
			Operation: op,
			Diff:      diff,
		}
		rc.config.OnChange(change, obj, oldObj)
	}

	change := ResourceChange{
		Kind:      kind,
		Namespace: ns,
		Name:      name,
		UID:       uid,
		Operation: op,
		Diff:      diff,
	}

	// Non-blocking send to changes channel
	select {
	case ch <- change:
	default:
		if rc.config.OnDrop != nil {
			rc.config.OnDrop(kind, ns, name, "channel_full", op)
		}
	}
}

// enqueueEvent handles K8s Event resource changes (separate path).
func (rc *ResourceCache) enqueueEvent(ch chan<- ResourceChange, obj any, op string) {
	meta, ok := obj.(metav1.Object)
	if !ok {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			meta, ok = tombstone.Obj.(metav1.Object)
			if !ok {
				return
			}
			obj = tombstone.Obj
		} else {
			return
		}
	}

	ns := meta.GetNamespace()
	name := meta.GetName()
	uid := string(meta.GetUID())

	// Fire OnEventChange callback
	if rc.config.OnEventChange != nil {
		rc.config.OnEventChange(obj, op)
	}

	change := ResourceChange{
		Kind:      "Event",
		Namespace: ns,
		Name:      name,
		UID:       uid,
		Operation: op,
	}

	select {
	case ch <- change:
	default:
		if rc.config.OnDrop != nil {
			rc.config.OnDrop("Event", ns, name, "channel_full", op)
		}
	}
}

// Stop initiates a non-blocking shutdown of the cache.
func (rc *ResourceCache) Stop() {
	if rc == nil {
		return
	}
	rc.stopOnce.Do(func() {
		log.Println("Stopping resource cache")
		close(rc.stopCh)
		go func() {
			done := make(chan struct{})
			go func() {
				rc.factory.Shutdown()
				close(done)
			}()
			select {
			case <-done:
				log.Println("Resource cache factory shutdown complete")
			case <-time.After(5 * time.Second):
				log.Println("Resource cache factory shutdown taking >5s, abandoning")
			}
		}()
	})
}

// Changes returns a read-only channel for resource change notifications.
func (rc *ResourceCache) Changes() <-chan ResourceChange {
	if rc == nil {
		return nil
	}
	return rc.changes
}

// ChangesRaw returns the bidirectional channel for internal use.
func (rc *ResourceCache) ChangesRaw() chan ResourceChange {
	if rc == nil {
		return nil
	}
	return rc.changes
}

// IsSyncComplete returns true after the initial critical informer sync.
func (rc *ResourceCache) IsSyncComplete() bool {
	if rc == nil {
		return false
	}
	return rc.syncComplete
}

// IsDeferredSynced returns true when all deferred informers have completed sync.
func (rc *ResourceCache) IsDeferredSynced() bool {
	if rc == nil {
		return false
	}
	select {
	case <-rc.deferredDone:
		return true
	default:
		return false
	}
}

// DeferredDone returns a channel that is closed when all deferred informers
// have completed their initial sync.
func (rc *ResourceCache) DeferredDone() <-chan struct{} {
	if rc == nil {
		return nil
	}
	return rc.deferredDone
}

// GetEnabledResources returns a copy of the enabled resources map.
func (rc *ResourceCache) GetEnabledResources() map[string]bool {
	if rc == nil {
		return nil
	}
	result := make(map[string]bool, len(rc.enabledResources))
	maps.Copy(result, rc.enabledResources)
	return result
}

// GetResourceCount returns total cached resources across core listers.
func (rc *ResourceCache) GetResourceCount() int {
	if rc == nil {
		return 0
	}
	count := 0
	type listerFunc func() int
	countFuncs := []listerFunc{
		func() int { return listCount(rc.Services()) },
		func() int { return listCount(rc.Pods()) },
		func() int { return listCount(rc.Nodes()) },
		func() int { return listCount(rc.Namespaces()) },
		func() int { return listCount(rc.Deployments()) },
		func() int { return listCount(rc.DaemonSets()) },
		func() int { return listCount(rc.StatefulSets()) },
		func() int { return listCount(rc.ReplicaSets()) },
		func() int { return listCount(rc.Ingresses()) },
	}
	for _, f := range countFuncs {
		count += f()
	}
	return count
}

// isEnabled returns true if the resource type has an informer running.
func (rc *ResourceCache) isEnabled(key string) bool {
	if rc == nil || rc.enabledResources == nil {
		return false
	}
	return rc.enabledResources[key]
}

// isReady returns true if the resource is enabled and, if deferred, synced.
func (rc *ResourceCache) isReady(key string) bool {
	if !rc.isEnabled(key) {
		return false
	}
	if rc.config.DeferredTypes == nil || !rc.config.DeferredTypes[key] {
		return true
	}
	rc.deferredMu.RLock()
	defer rc.deferredMu.RUnlock()
	return rc.deferredSynced[key]
}
