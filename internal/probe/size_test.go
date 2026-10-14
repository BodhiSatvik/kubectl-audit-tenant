package probe

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
)

func sized(cpu, memory string) corev1.ResourceRequirements {
	l := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)}
	return corev1.ResourceRequirements{Requests: l, Limits: l.DeepCopy()}
}

func limitRange(items ...corev1.LimitRangeItem) []corev1.LimitRange {
	return []corev1.LimitRange{{Spec: corev1.LimitRangeSpec{Limits: items}}}
}

func list(cpu, memory string) corev1.ResourceList {
	l := corev1.ResourceList{}
	if cpu != "" {
		l[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if memory != "" {
		l[corev1.ResourceMemory] = resource.MustParse(memory)
	}
	return l
}

func TestSizeFollowsTheLimitRange(t *testing.T) {
	defaults := corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Default: list("500m", "512Mi"), DefaultRequest: list("100m", "128Mi")}
	tests := []struct {
		name   string
		ranges []corev1.LimitRange
		err    error
		want   corev1.ResourceRequirements
	}{
		{"defaults for cpu and memory: still explicit sizes (D85)", limitRange(defaults), nil, sized("1m", "1Mi")},
		{"min only: the min", limitRange(corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Min: list("100m", "64Mi")}), nil, sized("100m", "64Mi")},
		{"a pod min counts too", limitRange(corev1.LimitRangeItem{Type: corev1.LimitTypePod, Min: list("", "32Mi")}), nil, sized("1m", "32Mi")},
		{"no LimitRange: 1m and 1Mi", nil, nil, sized("1m", "1Mi")},
		{"cannot list (403): 1m and 1Mi", limitRange(defaults), errors.New("forbidden"), sized("1m", "1Mi")},
		{"defaults for cpu only: sized, raised to the min", limitRange(corev1.LimitRangeItem{Type: corev1.LimitTypeContainer,
			Default: list("500m", ""), DefaultRequest: list("100m", ""), Min: list("", "16Mi")}), nil, sized("1m", "16Mi")},
		{"the higher of two mins", append(limitRange(corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Min: list("50m", "")}),
			limitRange(corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Min: list("200m", "")})...), nil, sized("200m", "1Mi")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Size(tt.ranges, tt.err); !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("Size() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSizeIgnoresServerFilledDefaults(t *testing.T) {
	// D85: the API server copies max into default and defaultRequest; following them would send the max.
	maxOnly := limitRange(corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Min: list("200m", "64Mi"), Max: list("2", "4Gi"), Default: list("2", "4Gi"), DefaultRequest: list("2", "4Gi")})
	if got := Size(maxOnly, nil); !equality.Semantic.DeepEqual(got, sized("200m", "64Mi")) {
		t.Errorf("Size() = %+v, want the min 200m/64Mi, never the max", got)
	}
}
