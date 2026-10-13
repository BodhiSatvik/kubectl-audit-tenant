package probe

import corev1 "k8s.io/api/core/v1"

// ControlService is a plain ClusterIP Service: it must be accepted before the NodePort probe
// can prove anything (ADR 0005).
func ControlService() *corev1.Service { return service(corev1.ServiceTypeClusterIP) }

// NodePortService is AT-05's probe.
func NodePortService() *corev1.Service { return service(corev1.ServiceTypeNodePort) }

func service(t corev1.ServiceType) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: meta(),
		Spec:       corev1.ServiceSpec{Type: t, Ports: []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}},
	}
}
