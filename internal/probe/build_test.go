package probe

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

func TestBuildControlPodIsRestrictedCompliant(t *testing.T) {
	res := sized("1m", "1Mi")
	p := ControlPod(res)
	if p.GenerateName != "audit-tenant-probe-" || p.Labels["app.kubernetes.io/managed-by"] != "audit-tenant" || p.Annotations["audit-tenant.dev/probe"] != "true" {
		t.Errorf("metadata = %+v, want generateName, the managed-by label and the probe annotation", p.ObjectMeta)
	}
	if len(p.Spec.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(p.Spec.Containers))
	}
	c := p.Spec.Containers[0]
	if c.Name != "probe" || c.Image != "registry.k8s.io/pause:3.10" || !equality.Semantic.DeepEqual(c.Resources, res) {
		t.Errorf("container = %s %s %+v, want probe, pause:3.10 and the given sizes", c.Name, c.Image, c.Resources)
	}
	sc := c.SecurityContext
	if sc == nil || !*sc.RunAsNonRoot || *sc.RunAsUser != 65532 || *sc.AllowPrivilegeEscalation ||
		len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("securityContext = %+v, want PSA restricted: non-root 65532, no escalation, drop ALL, RuntimeDefault", sc)
	}
	if sc.Privileged != nil || p.Spec.HostPID || p.Spec.HostNetwork || len(p.Spec.Volumes) != 0 {
		t.Errorf("control pod has a dangerous setting: %+v", p.Spec)
	}
}

func TestBuildEachProbeAddsExactlyItsDangerousSetting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*corev1.Pod)
		check  func(*corev1.Pod) bool
	}{
		{"AT-04.1 privileged, escalation left unset so validation accepts it (D70)", Privileged, func(p *corev1.Pod) bool {
			sc := p.Spec.Containers[0].SecurityContext
			return *sc.Privileged && sc.AllowPrivilegeEscalation == nil
		}},
		{"AT-04.2 hostPath volume", HostPath, func(p *corev1.Pod) bool {
			return len(p.Spec.Volumes) == 1 && p.Spec.Volumes[0].HostPath != nil && p.Spec.Volumes[0].HostPath.Path == "/"
		}},
		{"AT-04.3 hostPID", HostPID, func(p *corev1.Pod) bool { return p.Spec.HostPID }},
		{"AT-04.4 hostNetwork", HostNetwork, func(p *corev1.Pod) bool { return p.Spec.HostNetwork }},
		{"AT-04.1, second form: privileged with escalation allowed (D78)", PrivilegedWithEscalation, func(p *corev1.Pod) bool {
			sc := p.Spec.Containers[0].SecurityContext
			return *sc.Privileged && *sc.AllowPrivilegeEscalation
		}},
		{"AT-04.5 escalation only (D78)", Escalation, func(p *corev1.Pod) bool {
			sc := p.Spec.Containers[0].SecurityContext
			return *sc.AllowPrivilegeEscalation && *sc.RunAsUser == 65532 && *sc.RunAsNonRoot
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ControlPod(corev1.ResourceRequirements{})
			tt.mutate(p)
			if !tt.check(p) {
				t.Errorf("probe spec = %+v, want the dangerous setting", p.Spec)
			}
			if control := ControlPod(corev1.ResourceRequirements{}); equality.Semantic.DeepEqual(p, control) {
				t.Error("the probe equals the control pod")
			}
		})
	}
}

func TestBuildServices(t *testing.T) {
	for name, tt := range map[string]struct {
		svc  *corev1.Service
		kind corev1.ServiceType
	}{
		"control": {ControlService(), corev1.ServiceTypeClusterIP},
		"probe":   {NodePortService(), corev1.ServiceTypeNodePort},
	} {
		s := tt.svc
		if s.GenerateName != "audit-tenant-probe-" || s.Labels["app.kubernetes.io/managed-by"] != "audit-tenant" || s.Annotations["audit-tenant.dev/probe"] != "true" {
			t.Errorf("%s: metadata = %+v", name, s.ObjectMeta)
		}
		if s.Spec.Type != tt.kind || len(s.Spec.Ports) != 1 || s.Spec.Ports[0].Port != 80 || s.Spec.Ports[0].Protocol != corev1.ProtocolTCP || s.Spec.Selector != nil {
			t.Errorf("%s: spec = %+v, want %s, one port 80/TCP, no selector", name, s.Spec, tt.kind)
		}
	}
}

func TestBuildEachProbeChangesOnlyItsOwnSetting(t *testing.T) {
	// D78, D83: a probe proves something only if it differs from the control in its own setting.
	// The privileged forms must also touch allowPrivilegeEscalation (validation refuses false).
	for name, tt := range map[string]struct{ mutate, undo func(*corev1.Pod) }{
		"privileged, escalation unset": {Privileged, func(p *corev1.Pod) {
			sc := p.Spec.Containers[0].SecurityContext
			sc.Privileged, sc.AllowPrivilegeEscalation = nil, new(false)
		}},
		"privileged, escalation allowed": {PrivilegedWithEscalation, func(p *corev1.Pod) {
			sc := p.Spec.Containers[0].SecurityContext
			sc.Privileged, sc.AllowPrivilegeEscalation = nil, new(false)
		}},
		"hostPath":    {HostPath, func(p *corev1.Pod) { p.Spec.Volumes = nil }},
		"hostPID":     {HostPID, func(p *corev1.Pod) { p.Spec.HostPID = false }},
		"hostNetwork": {HostNetwork, func(p *corev1.Pod) { p.Spec.HostNetwork = false }},
		"escalation":  {Escalation, func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = new(false) }},
	} {
		p := ControlPod(corev1.ResourceRequirements{})
		tt.mutate(p)
		tt.undo(p)
		if !equality.Semantic.DeepEqual(p, ControlPod(corev1.ResourceRequirements{})) {
			t.Errorf("%s: the probe changes more than its own setting: %+v", name, p.Spec.Containers[0].SecurityContext)
		}
	}
}
