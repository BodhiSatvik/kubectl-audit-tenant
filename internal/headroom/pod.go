// Package headroom is the pure math of AT-10 (ADR 0006): one pod's requests and limits, a
// workload's rollout peak, and the comparison of the summed peak with a ResourceQuota. It reads
// k8s.io/api types and never talks to a cluster.
package headroom

import (
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// sized are the resources a pod's requests and limits are counted in.
var sized = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

// Defaults are the Container defaults the math uses, merged from every LimitRange.
type Defaults struct {
	Limit   corev1.ResourceList // default
	Request corev1.ResourceList // defaultRequest
	// limitFrom and requestFrom name the LimitRange that gave each value.
	limitFrom, requestFrom map[corev1.ResourceName]string
}

// DefaultsOf merges the Container defaults of every LimitRange: LimitRanger fills each missing
// value from every LimitRange, so each value here comes from the first by name that sets it
// (D112; the API server walks its cache, so two ranges that disagree make this an estimate).
func DefaultsOf(ranges []corev1.LimitRange) Defaults {
	d := Defaults{Limit: corev1.ResourceList{}, Request: corev1.ResourceList{},
		limitFrom: map[corev1.ResourceName]string{}, requestFrom: map[corev1.ResourceName]string{}}
	ranges = slices.SortedFunc(slices.Values(ranges), func(a, b corev1.LimitRange) int { return strings.Compare(a.Name, b.Name) })
	for _, lr := range ranges {
		for _, item := range lr.Spec.Limits {
			if item.Type == corev1.LimitTypeContainer {
				fill(d.Limit, d.limitFrom, item.Default, lr.Name)
				fill(d.Request, d.requestFrom, item.DefaultRequest, lr.Name)
			}
		}
	}
	return d
}

// fill copies into into each value of values that into lacks, noting in from that name gave it.
func fill(into corev1.ResourceList, from map[corev1.ResourceName]string, values corev1.ResourceList, name string) {
	for k, v := range values {
		if _, ok := into[k]; !ok {
			into[k], from[k] = v, name
		}
	}
}

// Pod is one pod's cpu and memory as the quota counts them.
type Pod struct {
	Requests, Limits corev1.ResourceList
	Unset            []corev1.ResourceName // quota keys (requests.cpu ... limits.memory) a container leaves unset, in Keys order; none with pod-level values (D132)
	Unsized          int                   // containers that leave a value unset: no value, no LimitRange default, no pod-level value
	Defaulted        int                   // containers that took a value from the LimitRange defaults
	DefaultedFrom    []string              // the LimitRanges those values came from, by name (D121)
	RuntimeClass     bool                  // the pod names a RuntimeClass, whose overhead is not counted
}

// PodOf follows the Kubernetes rules. Pod-level spec.resources, where set, are the pod's values.
// Otherwise each container's request is its request, else its limit (the API server copies a
// limit into a missing request), else the LimitRange defaultRequest, else 0; its limit is its
// limit, else the LimitRange default, else 0. App containers and sidecars (init containers with
// restartPolicy Always) add up; an init container runs with the sidecars declared before it; the
// pod needs the larger of the two, per resource. A value left unset counts as 0 and is listed in
// Unset, since a quota that tracks it refuses the pod (D111), unless the pod sets any pod-level
// value: quota admission then skips that check (D132).
func PodOf(spec corev1.PodSpec, d Defaults) Pod {
	var podLevel corev1.ResourceRequirements
	if spec.Resources != nil {
		podLevel = *spec.Resources
	}
	p := Pod{RuntimeClass: spec.RuntimeClassName != nil && *spec.RuntimeClassName != ""}
	unset := map[corev1.ResourceName]bool{}
	from := map[string]bool{} // the LimitRanges whose defaults a container took
	resolve := func(c corev1.Container) (requests, limits corev1.ResourceList) {
		requests, limits = corev1.ResourceList{}, corev1.ResourceList{}
		gap, defaulted := false, false
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
				if !podReq && !podLim {
					defaulted, from[d.requestFrom[r]] = true, true
				}
			}
			if v, ok := d.Limit[r]; !hasLim && ok {
				lim, hasLim = v, true
				if !podLim {
					defaulted, from[d.limitFrom[r]] = true, true
				}
			}
			// A gap counts only where the pod-level value does not replace the container sum.
			if !hasReq && !podReq && !podLim {
				unset["requests."+r], gap = true, true
			}
			if !hasLim && !podLim {
				unset["limits."+r], gap = true, true
			}
			requests[r], limits[r] = req, lim
		}
		if gap {
			p.Unsized++
		}
		if defaulted {
			p.Defaulted++
		}
		return requests, limits
	}
	var appReq, appLim, initReq, initLim []corev1.ResourceList
	var sidecar []bool
	for _, c := range spec.Containers {
		req, lim := resolve(c)
		appReq, appLim = append(appReq, req), append(appLim, lim)
	}
	for _, c := range spec.InitContainers {
		req, lim := resolve(c)
		initReq, initLim = append(initReq, req), append(initLim, lim)
		sidecar = append(sidecar, c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways)
	}
	p.Requests, p.Limits = effective(appReq, initReq, sidecar), effective(appLim, initLim, sidecar)
	// Quota admission skips "must specify" for a pod with any pod-level value; the API server
	// accepts only cpu, memory and hugepages there (D132).
	for _, k := range Keys {
		if unset[k] && len(podLevel.Requests)+len(podLevel.Limits) == 0 {
			p.Unset = append(p.Unset, k)
		}
	}
	delete(from, "") // a Defaults built by hand names no range
	p.DefaultedFrom = slices.Sorted(maps.Keys(from))
	var explicit corev1.ResourceList // the containers' own requests, before LimitRange defaults
	if len(podLevel.Limits) > 0 {
		explicit = PodOf(corev1.PodSpec{Containers: spec.Containers, InitContainers: spec.InitContainers}, Defaults{}).Requests
	}
	for _, r := range sized {
		if v, ok := podLevel.Limits[r]; ok {
			// SetDefaults_Pod: a missing pod-level request is the containers' summed requests, or the
			// limit when no container requests the resource (D110); a request of 0 counts (D132).
			p.Requests[r], p.Limits[r] = v, v
			if requested(spec, r) {
				p.Requests[r] = explicit[r]
			}
		}
		if v, ok := podLevel.Requests[r]; ok {
			p.Requests[r] = v
		}
	}
	return p
}

// requested reports whether a container of spec requests r; a limit counts, as the API server copies
// it into a missing request.
func requested(spec corev1.PodSpec, r corev1.ResourceName) bool {
	return slices.ContainsFunc(slices.Concat(spec.Containers, spec.InitContainers), func(c corev1.Container) bool {
		_, req := c.Resources.Requests[r]
		_, lim := c.Resources.Limits[r]
		return req || lim
	})
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
