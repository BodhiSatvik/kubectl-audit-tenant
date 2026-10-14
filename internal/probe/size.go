// Package probe builds the dry-run probes of AT-04 and AT-05 (ADR 0005) and classifies the
// cluster's answer to them.
package probe

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Size is the size every probe states (ADR 0005): 1m CPU and 1Mi memory for requests and limits,
// raised to the highest LimitRange min, and 1m and 1Mi when the LimitRanges cannot be listed (err,
// usually a 403). It never relies on LimitRange defaults: the API server copies max into default
// and defaultRequest, so a probe without sizes would ask for the max (D85).
func Size(ranges []corev1.LimitRange, err error) corev1.ResourceRequirements {
	size := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1m"), corev1.ResourceMemory: resource.MustParse("1Mi")}
	if err != nil {
		ranges = nil // cannot list (usually a 403): 1m and 1Mi
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
	return corev1.ResourceRequirements{Requests: size, Limits: size.DeepCopy()}
}
