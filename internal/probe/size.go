// Package probe builds the dry-run probes of AT-04 and AT-05 (ADR 0005) and classifies the
// cluster's answer to them.
package probe

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Size is the size every probe states (ADR 0005). A Container LimitRange with default and
// defaultRequest for cpu and memory: no size, so the server applies the namespace's own
// defaults. Otherwise 1m CPU and 1Mi memory for requests and limits, raised to the highest
// LimitRange min. When the LimitRanges cannot be listed (err, usually a 403): 1m and 1Mi.
func Size(ranges []corev1.LimitRange, err error) corev1.ResourceRequirements {
	size := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1m"), corev1.ResourceMemory: resource.MustParse("1Mi")}
	if err == nil {
		for _, lr := range ranges {
			for _, item := range lr.Spec.Limits {
				if item.Type == corev1.LimitTypeContainer && both(item.Default) && both(item.DefaultRequest) {
					return corev1.ResourceRequirements{}
				}
			}
		}
		for _, lr := range ranges {
			for _, item := range lr.Spec.Limits {
				for name, floor := range size {
					if m, ok := item.Min[name]; ok && m.Cmp(floor) > 0 {
						size[name] = m
					}
				}
			}
		}
	}
	return corev1.ResourceRequirements{Requests: size, Limits: size.DeepCopy()}
}

func both(l corev1.ResourceList) bool {
	_, cpu := l[corev1.ResourceCPU]
	_, memory := l[corev1.ResourceMemory]
	return cpu && memory
}
