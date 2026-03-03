package k8score

import (
	"k8s.io/apimachinery/pkg/labels"
	listersappsv1 "k8s.io/client-go/listers/apps/v1"
	listersautoscalingv2 "k8s.io/client-go/listers/autoscaling/v2"
	listersbatchv1 "k8s.io/client-go/listers/batch/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	listersnetworkingv1 "k8s.io/client-go/listers/networking/v1"
	listerspolicyv1 "k8s.io/client-go/listers/policy/v1"
	listersstoragev1 "k8s.io/client-go/listers/storage/v1"
)

func (rc *ResourceCache) Services() listerscorev1.ServiceLister {
	if rc == nil || !rc.isEnabled(Services) {
		return nil
	}
	return rc.factory.Core().V1().Services().Lister()
}

func (rc *ResourceCache) Pods() listerscorev1.PodLister {
	if rc == nil || !rc.isEnabled(Pods) {
		return nil
	}
	return rc.factory.Core().V1().Pods().Lister()
}

func (rc *ResourceCache) Nodes() listerscorev1.NodeLister {
	if rc == nil || !rc.isEnabled(Nodes) {
		return nil
	}
	return rc.factory.Core().V1().Nodes().Lister()
}

func (rc *ResourceCache) Namespaces() listerscorev1.NamespaceLister {
	if rc == nil || !rc.isEnabled(Namespaces) {
		return nil
	}
	return rc.factory.Core().V1().Namespaces().Lister()
}

func (rc *ResourceCache) ConfigMaps() listerscorev1.ConfigMapLister {
	if rc == nil || !rc.isReady(ConfigMaps) {
		return nil
	}
	return rc.factory.Core().V1().ConfigMaps().Lister()
}

func (rc *ResourceCache) Secrets() listerscorev1.SecretLister {
	if rc == nil || !rc.isReady(Secrets) {
		return nil
	}
	return rc.factory.Core().V1().Secrets().Lister()
}

func (rc *ResourceCache) Events() listerscorev1.EventLister {
	if rc == nil || !rc.isReady(Events) {
		return nil
	}
	return rc.factory.Core().V1().Events().Lister()
}

func (rc *ResourceCache) PersistentVolumeClaims() listerscorev1.PersistentVolumeClaimLister {
	if rc == nil || !rc.isReady(PersistentVolumeClaims) {
		return nil
	}
	return rc.factory.Core().V1().PersistentVolumeClaims().Lister()
}

func (rc *ResourceCache) PersistentVolumes() listerscorev1.PersistentVolumeLister {
	if rc == nil || !rc.isReady(PersistentVolumes) {
		return nil
	}
	return rc.factory.Core().V1().PersistentVolumes().Lister()
}

func (rc *ResourceCache) Deployments() listersappsv1.DeploymentLister {
	if rc == nil || !rc.isEnabled(Deployments) {
		return nil
	}
	return rc.factory.Apps().V1().Deployments().Lister()
}

func (rc *ResourceCache) DaemonSets() listersappsv1.DaemonSetLister {
	if rc == nil || !rc.isEnabled(DaemonSets) {
		return nil
	}
	return rc.factory.Apps().V1().DaemonSets().Lister()
}

func (rc *ResourceCache) StatefulSets() listersappsv1.StatefulSetLister {
	if rc == nil || !rc.isEnabled(StatefulSets) {
		return nil
	}
	return rc.factory.Apps().V1().StatefulSets().Lister()
}

func (rc *ResourceCache) ReplicaSets() listersappsv1.ReplicaSetLister {
	if rc == nil || !rc.isEnabled(ReplicaSets) {
		return nil
	}
	return rc.factory.Apps().V1().ReplicaSets().Lister()
}

func (rc *ResourceCache) Ingresses() listersnetworkingv1.IngressLister {
	if rc == nil || !rc.isEnabled(Ingresses) {
		return nil
	}
	return rc.factory.Networking().V1().Ingresses().Lister()
}

func (rc *ResourceCache) IngressClasses() listersnetworkingv1.IngressClassLister {
	if rc == nil || !rc.isEnabled(IngressClasses) {
		return nil
	}
	return rc.factory.Networking().V1().IngressClasses().Lister()
}

func (rc *ResourceCache) Jobs() listersbatchv1.JobLister {
	if rc == nil || !rc.isEnabled(Jobs) {
		return nil
	}
	return rc.factory.Batch().V1().Jobs().Lister()
}

func (rc *ResourceCache) CronJobs() listersbatchv1.CronJobLister {
	if rc == nil || !rc.isEnabled(CronJobs) {
		return nil
	}
	return rc.factory.Batch().V1().CronJobs().Lister()
}

func (rc *ResourceCache) HorizontalPodAutoscalers() listersautoscalingv2.HorizontalPodAutoscalerLister {
	if rc == nil || !rc.isEnabled(HorizontalPodAutoscalers) {
		return nil
	}
	return rc.factory.Autoscaling().V2().HorizontalPodAutoscalers().Lister()
}

func (rc *ResourceCache) StorageClasses() listersstoragev1.StorageClassLister {
	if rc == nil || !rc.isReady(StorageClasses) {
		return nil
	}
	return rc.factory.Storage().V1().StorageClasses().Lister()
}

func (rc *ResourceCache) PodDisruptionBudgets() listerspolicyv1.PodDisruptionBudgetLister {
	if rc == nil || !rc.isReady(PodDisruptionBudgets) {
		return nil
	}
	return rc.factory.Policy().V1().PodDisruptionBudgets().Lister()
}

func (rc *ResourceCache) ServiceAccounts() listerscorev1.ServiceAccountLister {
	if rc == nil || !rc.isEnabled(ServiceAccounts) {
		return nil
	}
	return rc.factory.Core().V1().ServiceAccounts().Lister()
}

// listCount is a helper that counts items from any lister that supports List(labels.Everything()).
func listCount(lister any) int {
	if lister == nil {
		return 0
	}
	type listable interface {
		List(selector labels.Selector) ([]any, error)
	}
	// Use type switches for known lister types since Go generics don't help here
	switch l := lister.(type) {
	case listerscorev1.ServiceLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listerscorev1.PodLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listerscorev1.NodeLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listerscorev1.NamespaceLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listersappsv1.DeploymentLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listersappsv1.DaemonSetLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listersappsv1.StatefulSetLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listersappsv1.ReplicaSetLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	case listersnetworkingv1.IngressLister:
		items, err := l.List(labels.Everything())
		if err != nil {
			return 0
		}
		return len(items)
	}
	return 0
}
