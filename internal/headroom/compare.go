package headroom

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Keys are the five quota keys AT-06 requires and AT-10 compares, in report order.
var Keys = []corev1.ResourceName{
	corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory, corev1.ResourceLimitsCPU, corev1.ResourceLimitsMemory, corev1.ResourcePods,
}

// aliases are the other spellings the API accepts for three of the Keys.
var aliases = map[corev1.ResourceName]corev1.ResourceName{
	corev1.ResourceCPU:    corev1.ResourceRequestsCPU,
	corev1.ResourceMemory: corev1.ResourceRequestsMemory,
	"count/pods":          corev1.ResourcePods,
}

// Canonical maps a quota key to the one of Keys it counts: cpu, memory and count/pods are
// aliases. Any other key maps to itself.
func Canonical(key corev1.ResourceName) corev1.ResourceName {
	if a, ok := aliases[key]; ok {
		return a
	}
	return key
}

// Total sums the workloads' peaks for each of the Keys: every pod of a workload's peak counts
// its pod's requests and limits; all workloads roll out at once (the worst case, ADR 0006).
func Total(ws []Workload) corev1.ResourceList {
	total := corev1.ResourceList{}
	pods := 0
	for _, w := range ws {
		n := int64(w.Peak)
		add(total, corev1.ResourceList{
			corev1.ResourceRequestsCPU:    times(w.Pod.Requests[corev1.ResourceCPU], n),
			corev1.ResourceRequestsMemory: times(w.Pod.Requests[corev1.ResourceMemory], n),
			corev1.ResourceLimitsCPU:      times(w.Pod.Limits[corev1.ResourceCPU], n),
			corev1.ResourceLimitsMemory:   times(w.Pod.Limits[corev1.ResourceMemory], n),
		})
		pods += w.Peak
	}
	total[corev1.ResourcePods] = *resource.NewQuantity(int64(pods), resource.DecimalSI)
	return total
}

// Shortfall is one quota key whose hard value the total exceeds.
type Shortfall struct {
	Key        corev1.ResourceName // as the quota spells it
	Need, Hard resource.Quantity
}

// String reads "requests.cpu short by 2: peak needs 6, hard is 4": the number to act on first,
// where the checklist's cut cannot hide it (S2-F3).
func (s Shortfall) String() string {
	short := s.Need.DeepCopy()
	short.Sub(s.Hard)
	return fmt.Sprintf("%s short by %s: peak needs %s, hard is %s", s.Key, short.String(), s.Need.String(), s.Hard.String())
}

// QuotaKeys returns the keys of q's hard that count as one of the Keys, as q spells them, in
// Keys order, the canonical spelling before its alias. Other keys are not compared.
func QuotaKeys(q corev1.ResourceQuota) []corev1.ResourceName {
	var out []corev1.ResourceName
	for _, k := range Keys {
		for _, key := range spellings(k) {
			if _, ok := q.Spec.Hard[key]; ok {
				out = append(out, key)
			}
		}
	}
	return out
}

// Compare returns each of q's QuotaKeys whose hard value total exceeds.
func Compare(q corev1.ResourceQuota, total corev1.ResourceList) []Shortfall {
	var out []Shortfall
	for _, key := range QuotaKeys(q) {
		k := Canonical(key)
		if need, hard := total[k], q.Spec.Hard[key]; need.Cmp(hard) > 0 {
			out = append(out, Shortfall{Key: key, Need: need, Hard: hard})
		}
	}
	return out
}

// spellings are the keys that count as k: k itself, then its alias if it has one.
func spellings(k corev1.ResourceName) []corev1.ResourceName {
	out := []corev1.ResourceName{k}
	for alias, canonical := range aliases {
		if canonical == k {
			out = append(out, alias)
		}
	}
	return out
}

// Breakdown lists replicas->peak per workload, by kind: "deployments: api 3->4, worker 2->3;
// statefulsets: db 3->3". A count from an HPA says so.
func Breakdown(ws []Workload) string {
	var groups []string
	for _, kind := range []string{"Deployment", "StatefulSet"} {
		var items []string
		for _, w := range ws {
			if w.Kind != kind {
				continue
			}
			item := fmt.Sprintf("%s %d->%d", w.Name, w.Replicas, w.Peak)
			if w.HPA != "" {
				item += " (HPA max)"
			}
			items = append(items, item)
		}
		if len(items) > 0 {
			groups = append(groups, strings.ToLower(kind)+"s: "+strings.Join(items, ", "))
		}
	}
	return strings.Join(groups, "; ")
}
