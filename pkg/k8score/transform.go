package k8score

import (
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DropManagedFields is a SharedInformer transform that reduces memory usage
// by removing managedFields and heavy annotations from cached objects.
// This is the union of transforms used by both Radar and skyhook-connector.
func DropManagedFields(obj any) (any, error) {
	if meta, ok := obj.(metav1.Object); ok {
		meta.SetManagedFields(nil)
	}

	// Special handling for Events — aggressively strip to essentials
	if event, ok := obj.(*corev1.Event); ok {
		return &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:              event.Name,
				Namespace:         event.Namespace,
				UID:               event.UID,
				ResourceVersion:   event.ResourceVersion,
				CreationTimestamp: event.CreationTimestamp,
			},
			InvolvedObject: event.InvolvedObject,
			Reason:         event.Reason,
			Message:        event.Message,
			Type:           event.Type,
			Count:          event.Count,
			FirstTimestamp: event.FirstTimestamp,
			LastTimestamp:  event.LastTimestamp,
		}, nil
	}

	// Drop heavy annotations from common resources
	switch obj.(type) {
	case *corev1.Pod, *corev1.Service, *corev1.Node, *corev1.Namespace,
		*corev1.PersistentVolumeClaim, *corev1.PersistentVolume,
		*corev1.ConfigMap, *corev1.Secret, *corev1.ServiceAccount,
		*appsv1.Deployment, *appsv1.DaemonSet, *appsv1.StatefulSet, *appsv1.ReplicaSet,
		*networkingv1.Ingress, *networkingv1.IngressClass,
		*batchv1.Job, *batchv1.CronJob,
		*autoscalingv2.HorizontalPodAutoscaler,
		*policyv1.PodDisruptionBudget, *storagev1.StorageClass:
		if meta, ok := obj.(metav1.Object); ok && meta.GetAnnotations() != nil {
			delete(meta.GetAnnotations(), "kubectl.kubernetes.io/last-applied-configuration")
		}
	}

	return obj, nil
}
