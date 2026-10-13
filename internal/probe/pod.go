package probe

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// meta marks every probe object, so an audit log reader can tell it apart (DESIGN.md section 5).
func meta() metav1.ObjectMeta {
	return metav1.ObjectMeta{
		GenerateName: "audit-tenant-probe-",
		Labels:       map[string]string{"app.kubernetes.io/managed-by": "audit-tenant"},
		Annotations:  map[string]string{"audit-tenant.dev/probe": "true"},
	}
}

// ControlPod is a pod that PSA restricted accepts: the baseline every dangerous probe changes
// in exactly one way. Dry-run never pulls the image.
func ControlPod(res corev1.ResourceRequirements) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: meta(),
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:      "probe",
			Image:     "registry.k8s.io/pause:3.10",
			Resources: res,
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             new(true),
				RunAsUser:                new(int64(65532)),
				AllowPrivilegeEscalation: new(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
		}}},
	}
}

// Privileged makes AT-04.1's probe. Escalation must be allowed too, or API validation rejects
// the pod before admission sees it (ADR 0005).
func Privileged(p *corev1.Pod) {
	sc := p.Spec.Containers[0].SecurityContext
	sc.Privileged, sc.AllowPrivilegeEscalation = new(true), new(true)
}

// HostPath makes AT-04.2's probe: a hostPath volume of the node's root.
func HostPath(p *corev1.Pod) {
	p.Spec.Volumes = []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}}
}

// HostPID makes AT-04.3's probe.
func HostPID(p *corev1.Pod) { p.Spec.HostPID = true }

// HostNetwork makes AT-04.4's probe.
func HostNetwork(p *corev1.Pod) { p.Spec.HostNetwork = true }

// RootWithEscalation makes AT-04.5's probe: one capability, running as root with escalation
// allowed, takes three fields.
func RootWithEscalation(p *corev1.Pod) {
	sc := p.Spec.Containers[0].SecurityContext
	sc.AllowPrivilegeEscalation, sc.RunAsUser, sc.RunAsNonRoot = new(true), new(int64(0)), new(false)
}
