package headroom

import (
	"math"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Workload is one counted workload and its rollout peak.
type Workload struct {
	Kind     string // Deployment or StatefulSet
	Name     string
	Replicas int    // spec.replicas (default 1), or the maxReplicas of an HPA that targets it
	HPA      string // that HPA, or empty
	Peak     int    // pods while it rolls out
	Pod      Pod
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

// workload sets the replica count: spec.replicas (default 1), or the largest maxReplicas of the
// HPAs whose scaleTargetRef names this workload by API group (apps), kind and name.
func workload(kind, name string, replicas *int32, hpas []autoscalingv2.HorizontalPodAutoscaler) Workload {
	w := Workload{Kind: kind, Name: name, Replicas: 1}
	if replicas != nil {
		w.Replicas = int(*replicas)
	}
	found := false
	for _, h := range hpas {
		ref := h.Spec.ScaleTargetRef
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil || gv.Group != appsv1.GroupName || ref.Kind != kind || ref.Name != name {
			continue
		}
		if maxReplicas := int(h.Spec.MaxReplicas); !found || maxReplicas > w.Replicas {
			w.Replicas, w.HPA, found = maxReplicas, h.Name, true
		}
	}
	return w
}
