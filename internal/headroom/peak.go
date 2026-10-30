package headroom

import (
	"math"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Workload is one counted workload and its rollout peak.
type Workload struct {
	Kind     string // Deployment or StatefulSet
	Name     string
	Replicas int    // spec.replicas (default 1); the maxReplicas of the one HPA that targets it; with several, spec.replicas raised to their largest minReplicas (D126)
	HPA      string // that HPA, or empty
	// ConflictingHPAs are the HPAs, by name, when several target this workload; none scales it on metrics.
	ConflictingHPAs []string
	Peak            int // pods while it rolls out
	Pod             Pod
}

// defaultSurge is the maxSurge the API server sets on a Deployment that names none.
var defaultSurge = intstr.FromString("25%")

// Deployment is d's peak: replicas plus maxSurge (absolute, or a percent of replicas rounded
// up) for RollingUpdate, replicas for Recreate.
func Deployment(d appsv1.Deployment, hpas []autoscalingv2.HorizontalPodAutoscaler, defaults Defaults) Workload {
	w := workload("Deployment", d.Name, d.Spec.Replicas, hpas)
	w.Pod = PodOf(d.Spec.Template.Spec, defaults)
	w.Peak = w.Replicas
	if d.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType || w.Replicas == 0 {
		return w
	}
	surge := defaultSurge
	if ru := d.Spec.Strategy.RollingUpdate; ru != nil && ru.MaxSurge != nil {
		surge = *ru.MaxSurge
	}
	// An invalid value cannot be stored (API validation), so the error is never set. The API
	// bounds no maxSurge percent, and a huge one scales out of int range; any surge past
	// MaxInt32 is far over every quota, so it is clamped there and the peak cannot wrap.
	extra, _ := intstr.GetScaledValueFromIntOrPercent(&surge, w.Replicas, true)
	if extra < 0 || extra > math.MaxInt32 {
		extra = math.MaxInt32
	}
	w.Peak += extra
	return w
}

// StatefulSet is s's peak: its replicas, since a StatefulSet replaces pods one at a time.
func StatefulSet(s appsv1.StatefulSet, hpas []autoscalingv2.HorizontalPodAutoscaler, defaults Defaults) Workload {
	w := workload("StatefulSet", s.Name, s.Spec.Replicas, hpas)
	w.Pod = PodOf(s.Spec.Template.Spec, defaults)
	w.Peak = w.Replicas
	return w
}

// workload sets the replica count: spec.replicas (default 1), or the maxReplicas of the one HPA
// whose scaleTargetRef names this workload by API group (apps), kind and name. When several HPAs
// target it, the HPA controller does not scale on metrics (AmbiguousSelector, proven on kind, D126)
// but still moves replicas up to a minReplicas or down to a maxReplicas, so replicas never pass
// spec.replicas raised to their largest minReplicas: that is the count, and ConflictingHPAs names them.
func workload(kind, name string, replicas *int32, hpas []autoscalingv2.HorizontalPodAutoscaler) Workload {
	w := Workload{Kind: kind, Name: name, Replicas: 1}
	if replicas != nil {
		w.Replicas = int(*replicas)
	}
	var matched []autoscalingv2.HorizontalPodAutoscaler
	for _, h := range hpas {
		ref := h.Spec.ScaleTargetRef
		if gv, err := schema.ParseGroupVersion(ref.APIVersion); err == nil && gv.Group == appsv1.GroupName && ref.Kind == kind && ref.Name == name {
			matched = append(matched, h)
		}
	}
	switch len(matched) {
	case 0:
	case 1:
		w.Replicas, w.HPA = int(matched[0].Spec.MaxReplicas), matched[0].Name
	default:
		for _, h := range matched {
			if h.Spec.MinReplicas != nil && int(*h.Spec.MinReplicas) > w.Replicas {
				w.Replicas = int(*h.Spec.MinReplicas)
			}
			w.ConflictingHPAs = append(w.ConflictingHPAs, h.Name)
		}
		slices.Sort(w.ConflictingHPAs)
	}
	return w
}
