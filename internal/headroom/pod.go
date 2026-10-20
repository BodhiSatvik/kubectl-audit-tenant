// Package headroom is the pure math of AT-10 (ADR 0006): one pod's requests and limits, a
// workload's rollout peak, and the comparison of the summed peak with a ResourceQuota. It reads
// k8s.io/api types and never talks to a cluster.
package headroom

import (
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// sized are the resources a pod's requests and limits are counted in.
var sized = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

// Defaults are the Container defaults the math uses, merged from every LimitRange.
type Defaults struct {
	Names   []string            // the LimitRanges that gave a value, by name
	Limit   corev1.ResourceList // default
	Request corev1.ResourceList // defaultRequest
}

// DefaultsOf merges the Container defaults of every LimitRange: LimitRanger fills each missing
// value from every LimitRange, so each value here comes from the first by name that sets it
// (D112; the API server walks its cache, so two ranges that disagree make this an estimate).
func DefaultsOf(ranges []corev1.LimitRange) Defaults {
	d := Defaults{Limit: corev1.ResourceList{}, Request: corev1.ResourceList{}}
	ranges = slices.SortedFunc(slices.Values(ranges), func(a, b corev1.LimitRange) int { return strings.Compare(a.Name, b.Name) })
	for _, lr := range ranges {
		gave := false
		for _, item := range lr.Spec.Limits {
			if item.Type == corev1.LimitTypeContainer {
				gave = fill(d.Limit, item.Default) || gave
				gave = fill(d.Request, item.DefaultRequest) || gave
			}
		}
		if gave {
			d.Names = append(d.Names, lr.Name)
		}
	}
	return d
}

// fill copies into into each value of from that into lacks, and reports whether it copied one.
func fill(into, from corev1.ResourceList) bool {
	copied := false
	for k, v := range from {
		if _, ok := into[k]; !ok {
			into[k], copied = v, true
		}
	}
	return copied
}

// Pod is one pod's cpu and memory as the quota counts them.
type Pod struct {
	Requests, Limits corev1.ResourceList
	Unsized          int  // containers with a request or limit counted as 0: no value and no LimitRange default
	Defaulted        int  // containers that took a value from the LimitRange defaults
	RuntimeClass     bool // the pod names a RuntimeClass, whose overhead is not counted
}

// PodOf follows the Kubernetes rules. Pod-level spec.resources, where set, are the pod's values.
// Otherwise each container's request is its request, else its limit (the API server copies a
// limit into a missing request), else the LimitRange defaultRequest, else 0; its limit is its
// limit, else the LimitRange default, else 0. App containers and sidecars (init containers with
// restartPolicy Always) add up; an init container runs with the sidecars declared before it; the
// pod needs the larger of the two, per resource.
func PodOf(spec corev1.PodSpec, d Defaults) Pod {
	var podLevel corev1.ResourceRequirements
	if spec.Resources != nil {
		podLevel = *spec.Resources
	}
	p := Pod{RuntimeClass: spec.RuntimeClassName != nil && *spec.RuntimeClassName != ""}
	type sizes struct{ requests, limits corev1.ResourceList }
	resolve := func(c corev1.Container) sizes {
		s := sizes{corev1.ResourceList{}, corev1.ResourceList{}}
		unsized, defaulted := false, false
		for _, r := range sized {
			_, podReq := podLevel.Requests[r]
			_, podLim := podLevel.Limits[r]
			req, hasReq := c.Resources.Requests[r]
			lim, hasLim := c.Resources.Limits[r]
			if !hasReq && hasLim {
				req, hasReq = lim, true
			}
			if v, ok := d.Request[r]; !hasReq && ok {
				req, hasReq = v, true
				defaulted = defaulted || (!podReq && !podLim)
			}
			if v, ok := d.Limit[r]; !hasLim && ok {
				lim, hasLim = v, true
				defaulted = defaulted || !podLim
			}
			// A gap counts only where the pod-level value does not replace the container sum.
			unsized = unsized || (!hasReq && !podReq && !podLim) || (!hasLim && !podLim)
			s.requests[r], s.limits[r] = req, lim
		}
		if unsized {
			p.Unsized++
		}
		if defaulted {
			p.Defaulted++
		}
		return s
	}
	var apps, inits []sizes
	var sidecar []bool
	for _, c := range spec.Containers {
		apps = append(apps, resolve(c))
	}
	for _, c := range spec.InitContainers {
		inits = append(inits, resolve(c))
		sidecar = append(sidecar, c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways)
	}
	pick := func(get func(sizes) corev1.ResourceList) corev1.ResourceList {
		var a, i []corev1.ResourceList
		for _, s := range apps {
			a = append(a, get(s))
		}
		for _, s := range inits {
			i = append(i, get(s))
		}
		return effective(a, i, sidecar)
	}
	p.Requests, p.Limits = pick(func(s sizes) corev1.ResourceList { return s.requests }), pick(func(s sizes) corev1.ResourceList { return s.limits })
	var explicit corev1.ResourceList // the containers' own requests, before LimitRange defaults
	if len(podLevel.Limits) > 0 {
		explicit = PodOf(corev1.PodSpec{Containers: spec.Containers, InitContainers: spec.InitContainers}, Defaults{}).Requests
	}
	for _, r := range sized {
		if v, ok := podLevel.Limits[r]; ok {
			// SetDefaults_Pod: a missing pod-level request is the containers' summed requests, or the
			// limit when no container requests the resource (D110).
			p.Requests[r], p.Limits[r] = v, v
			if q := explicit[r]; !q.IsZero() {
				p.Requests[r] = q
			}
		}
		if v, ok := podLevel.Requests[r]; ok {
			p.Requests[r] = v
		}
	}
	return p
}

// effective is the Kubernetes rule for one pod's value of each resource: app containers and
// sidecars add up; each init container runs with the sidecars started before it (a sidecar with
// itself too); the pod needs the larger of the two.
func effective(apps, inits []corev1.ResourceList, sidecar []bool) corev1.ResourceList {
	sum := corev1.ResourceList{}
	for _, l := range apps {
		add(sum, l)
	}
	running, initPeak := corev1.ResourceList{}, corev1.ResourceList{}
	for i, l := range inits {
		alone := running.DeepCopy()
		add(alone, l)
		if sidecar[i] {
			add(sum, l)
			add(running, l)
		}
		maxInto(initPeak, alone)
	}
	maxInto(sum, initPeak)
	return sum
}

func add(into, l corev1.ResourceList) {
	for k, v := range l {
		q := into[k]
		q.Add(v)
		into[k] = q
	}
}

func maxInto(into, l corev1.ResourceList) {
	for k, v := range l {
		if q, ok := into[k]; !ok || v.Cmp(q) > 0 {
			into[k] = v.DeepCopy()
		}
	}
}

// times is q added n times. Quantity.Mul falls back to arbitrary precision, so a huge peak
// cannot wrap negative and pass (D110).
func times(q resource.Quantity, n int64) resource.Quantity {
	out := q.DeepCopy()
	if out.Format == "" { // a value counted as 0 was never parsed
		out.Format = resource.DecimalSI
	}
	out.Mul(n)
	return out
}
