package headroom

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// list builds a ResourceList from "cpu", "100m", "memory", "128Mi" pairs.
func list(kv ...string) corev1.ResourceList {
	l := corev1.ResourceList{}
	for pair := range slices.Chunk(kv, 2) {
		l[corev1.ResourceName(pair[0])] = resource.MustParse(pair[1])
	}
	return l
}

func container(name string, requests, limits corev1.ResourceList) corev1.Container {
	return corev1.Container{Name: name, Resources: corev1.ResourceRequirements{Requests: requests, Limits: limits}}
}

func sidecar(c corev1.Container) corev1.Container {
	always := corev1.ContainerRestartPolicyAlways
	c.RestartPolicy = &always
	return c
}

// same reports whether two lists hold the same keys with equal quantities.
func same(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v.Cmp(w) != 0 {
			return false
		}
	}
	return true
}

func TestPodOf(t *testing.T) {
	sized := container("app", list("cpu", "100m", "memory", "128Mi"), list("cpu", "500m", "memory", "512Mi"))
	tests := []struct {
		name               string
		spec               corev1.PodSpec
		defaults           Defaults
		requests, limits   corev1.ResourceList
		unsized, defaulted int
	}{
		{"one sized container", corev1.PodSpec{Containers: []corev1.Container{sized}}, Defaults{},
			list("cpu", "100m", "memory", "128Mi"), list("cpu", "500m", "memory", "512Mi"), 0, 0},
		{"app containers add up", corev1.PodSpec{Containers: []corev1.Container{sized, sized}}, Defaults{},
			list("cpu", "200m", "memory", "256Mi"), list("cpu", "1", "memory", "1Gi"), 0, 0},
		{"a limit with no request counts as the request", corev1.PodSpec{Containers: []corev1.Container{container("app", nil, list("cpu", "1", "memory", "1Gi"))}}, Defaults{},
			list("cpu", "1", "memory", "1Gi"), list("cpu", "1", "memory", "1Gi"), 0, 0},
		{"no sizes and no LimitRange defaults count as 0", corev1.PodSpec{Containers: []corev1.Container{container("app", nil, nil), sized}}, Defaults{},
			list("cpu", "100m", "memory", "128Mi"), list("cpu", "500m", "memory", "512Mi"), 1, 0},
		{"LimitRange defaults fill what is missing", corev1.PodSpec{Containers: []corev1.Container{container("app", list("cpu", "50m"), nil)}},
			Defaults{Name: "limits", Request: list("cpu", "100m", "memory", "128Mi"), Limit: list("cpu", "500m", "memory", "512Mi")},
			list("cpu", "50m", "memory", "128Mi"), list("cpu", "500m", "memory", "512Mi"), 0, 1},
		{"an explicit limit beats defaultRequest for the request", corev1.PodSpec{Containers: []corev1.Container{container("app", nil, list("cpu", "2"))}},
			Defaults{Name: "limits", Request: list("cpu", "100m", "memory", "128Mi"), Limit: list("cpu", "500m", "memory", "512Mi")},
			list("cpu", "2", "memory", "128Mi"), list("cpu", "2", "memory", "512Mi"), 0, 1},
		{"a plain init container runs alone: the larger side wins per resource", corev1.PodSpec{
			InitContainers: []corev1.Container{container("migrate", list("cpu", "1", "memory", "64Mi"), list("cpu", "1", "memory", "64Mi"))},
			Containers:     []corev1.Container{sized}}, Defaults{},
			list("cpu", "1", "memory", "128Mi"), list("cpu", "1", "memory", "512Mi"), 0, 0},
		{"a sidecar adds to the app containers", corev1.PodSpec{
			InitContainers: []corev1.Container{sidecar(container("proxy", list("cpu", "50m", "memory", "64Mi"), list("cpu", "100m", "memory", "128Mi")))},
			Containers:     []corev1.Container{sized}}, Defaults{},
			list("cpu", "150m", "memory", "192Mi"), list("cpu", "600m", "memory", "640Mi"), 0, 0},
		{"an init container after a sidecar overlaps with it", corev1.PodSpec{
			InitContainers: []corev1.Container{
				sidecar(container("proxy", list("cpu", "300m", "memory", "64Mi"), list("cpu", "300m", "memory", "64Mi"))),
				container("migrate", list("cpu", "900m", "memory", "64Mi"), list("cpu", "900m", "memory", "64Mi")),
			},
			Containers: []corev1.Container{sized}}, Defaults{},
			// requests: apps+sidecar = 400m/192Mi; migrate+proxy = 1200m/128Mi. Limits: 800m/576Mi vs 1200m/128Mi.
			list("cpu", "1200m", "memory", "192Mi"), list("cpu", "1200m", "memory", "576Mi"), 0, 0},
		{"an init container before a sidecar does not overlap with it", corev1.PodSpec{
			InitContainers: []corev1.Container{
				container("migrate", list("cpu", "900m", "memory", "64Mi"), list("cpu", "900m", "memory", "64Mi")),
				sidecar(container("proxy", list("cpu", "300m", "memory", "64Mi"), list("cpu", "300m", "memory", "64Mi"))),
			},
			Containers: []corev1.Container{sized}}, Defaults{},
			list("cpu", "900m", "memory", "192Mi"), list("cpu", "900m", "memory", "576Mi"), 0, 0},
		{"pod-level resources replace the container sum", corev1.PodSpec{
			Resources:  &corev1.ResourceRequirements{Requests: list("cpu", "1", "memory", "1Gi"), Limits: list("cpu", "2", "memory", "2Gi")},
			Containers: []corev1.Container{sized, container("unsized", nil, nil)}}, Defaults{},
			list("cpu", "1", "memory", "1Gi"), list("cpu", "2", "memory", "2Gi"), 0, 0},
		{"pod-level resources for cpu only: memory still adds up the containers", corev1.PodSpec{
			Resources:  &corev1.ResourceRequirements{Limits: list("cpu", "2")},
			Containers: []corev1.Container{sized, container("no-memory", list("cpu", "1"), list("cpu", "1"))}}, Defaults{},
			list("cpu", "2", "memory", "128Mi"), list("cpu", "2", "memory", "512Mi"), 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PodOf(tt.spec, tt.defaults)
			if !same(p.Requests, tt.requests) || !same(p.Limits, tt.limits) || p.Unsized != tt.unsized || p.Defaulted != tt.defaulted {
				t.Errorf("PodOf() = requests %v, limits %v, unsized %d, defaulted %d;\nwant requests %v, limits %v, unsized %d, defaulted %d",
					p.Requests, p.Limits, p.Unsized, p.Defaulted, tt.requests, tt.limits, tt.unsized, tt.defaulted)
			}
		})
	}
}

func TestPodOfNotesARuntimeClass(t *testing.T) {
	gvisor := "gvisor"
	if p := PodOf(corev1.PodSpec{RuntimeClassName: &gvisor}, Defaults{}); !p.RuntimeClass {
		t.Error("a pod with a RuntimeClass must say so: its overhead is not counted")
	}
	if p := PodOf(corev1.PodSpec{}, Defaults{}); p.RuntimeClass {
		t.Error("a pod without a RuntimeClass has no overhead to note")
	}
}

func TestDefaultsOf(t *testing.T) {
	lr := func(name string, items ...corev1.LimitRangeItem) corev1.LimitRange {
		return corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: corev1.LimitRangeSpec{Limits: items}}
	}
	podMax := corev1.LimitRangeItem{Type: corev1.LimitTypePod, Max: list("cpu", "4")}
	b := corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Default: list("cpu", "1"), DefaultRequest: list("cpu", "500m")}
	a := corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, Default: list("cpu", "2")}
	got := DefaultsOf([]corev1.LimitRange{lr("b-defaults", b), lr("pod-only", podMax), lr("a-defaults", podMax, a)})
	if got.Name != "a-defaults" || !same(got.Limit, a.Default) || len(got.Request) != 0 {
		t.Errorf("DefaultsOf() = %+v; want the first by name with Container defaults, a-defaults", got)
	}
	if got := DefaultsOf([]corev1.LimitRange{lr("pod-only", podMax)}); got.Name != "" {
		t.Errorf("DefaultsOf() = %+v; want none: no Container item has defaults", got)
	}
}
