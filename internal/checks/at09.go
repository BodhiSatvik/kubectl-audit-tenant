package checks

import (
	"context"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

const psaEnforce = "pod-security.kubernetes.io/enforce"

// at09 reads the PSA enforce label from the namespace startup fetched. Only baseline or restricted
// passes, even when another gate blocks dangerous pods (ADR 0007): the FAIL reason then says what
// AT-04 showed. A PASS asks whether the identity may update or patch its namespace, or its status
// or finalize subresource, which would let it remove the label; the answer is a note, not a
// verdict (D105, D119).
func at09(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	ns := c.Namespace
	switch {
	case apierrors.IsForbidden(c.NamespaceErr):
		return spec.all(report.Skip, "-", "cannot read namespace labels (forbidden)", "grant `get` on namespace "+ns+" to this identity, or run with --as <admin>")
	case c.NamespaceErr != nil:
		return spec.all(report.Error, "-", "could not read namespace "+ns+": "+c.NamespaceErr.Error(), hintRequestFailed)
	}
	r := spec.Results[0]
	level, labeled := c.NamespaceObject.Labels[psaEnforce]
	if level == "baseline" || level == "restricted" {
		pass := spec.result(r, report.Pass, layerConfig, psaEnforce+"="+level+" on namespace "+ns, "")
		// The status and finalize subresources change labels too (proven on kind, D119).
		for _, q := range []question{
			{verb: "update", resource: "namespaces", name: ns, namespace: ns, label: "update namespace " + ns},
			{verb: "patch", resource: "namespaces", name: ns, namespace: ns, label: "patch namespace " + ns},
			{verb: "update", resource: "namespaces", subresource: "status", name: ns, namespace: ns, label: "update namespace " + ns + "/status"},
			{verb: "patch", resource: "namespaces", subresource: "status", name: ns, namespace: ns, label: "patch namespace " + ns + "/status"},
			{verb: "update", resource: "namespaces", subresource: "finalize", name: ns, namespace: ns, label: "update namespace " + ns + "/finalize"},
		} {
			may, err := can(ctx, c, q)
			switch {
			case err != nil && ctx.Err() != nil:
				return nil
			case err != nil:
				pass.Note = "not tested: whether this identity may change this label: " + err.Error()
				return []report.Result{pass}
			case may.allowed:
				pass.Note = "this identity may " + q.label + ", so it can change or remove this label"
				return []report.Result{pass}
			}
		}
		return []report.Result{pass}
	}
	reason := "namespace " + ns + " has no " + psaEnforce + " label"
	if labeled {
		reason = psaEnforce + "=" + level + " on namespace " + ns + "; only baseline or restricted blocks dangerous pods"
	}
	switch layers := at04Admission(spec.earlier); {
	case slices.Contains(layers, probe.LayerPSA): // PSA rejecting any probe without the label proves a cluster default (D122)
		shows := "PSA"
		if len(layers) > 1 {
			shows = strings.Join(layers, ", ")
		}
		reason += "; AT-04 shows " + shows + " blocking every probe anyway, so a cluster-wide default level applies; the label is what states the level on the namespace"
	case len(layers) > 0:
		reason += "; AT-04 shows " + strings.Join(layers, ", ") + " blocking every probe anyway, so the gap is the label: the built-in gate as a second fence"
	}
	return []report.Result{spec.result(r, report.Fail, layerConfig, reason,
		"kubectl label --overwrite namespace "+ns+" "+psaEnforce+"=restricted (baseline also passes, but does not block AT-04.5)")}
}

// at04Admission returns the layers of AT-04's results when admission rejected every AT-04 probe
// (each result a PASS by an admission layer), in order and without repeats; otherwise nil. A run
// that skipped some of AT-04 cannot speak for every probe (D110).
func at04Admission(earlier []report.Result) []string {
	var layers []string
	probes := 0
	for _, r := range earlier {
		if !strings.HasPrefix(r.ID, "AT-04.") {
			continue
		}
		if r.Verdict != report.Pass || !strings.HasPrefix(r.Layer, "admission") {
			return nil
		}
		probes++
		if !slices.Contains(layers, r.Layer) {
			layers = append(layers, r.Layer)
		}
	}
	if probes < len(at04Results) {
		return nil
	}
	return layers
}
