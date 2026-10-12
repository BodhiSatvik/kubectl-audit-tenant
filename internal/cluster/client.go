// Package cluster builds the per-run connection: the clientset behind the safety transport, the
// startup sequence of DESIGN.md section 6, the identity for the header, and the one-list-per-kind cache.
package cluster

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/safety"
)

// Attributes is one "may I <verb> <resource>?" question for a SelfSubjectAccessReview.
type Attributes = authzv1.ResourceAttributes

// Options is what the CLI knows before the first request.
type Options struct {
	Namespace      string
	OtherNamespace string           // --other-namespace, the namespace AT-02 asks about
	Context        string           // kubeconfig context name, for messages and the header
	User           string           // kubeconfig user name, shown when the identity cannot be verified
	As             string           // --as; empty in self mode
	AsGroups       []string         // --as-group
	Timeout        time.Duration    // the run deadline, for the timeout message
	Recorder       *safety.Recorder // the caller's recorder, so --debug keeps the trace when startup fails; nil makes one
}

// Client is one run's connection. Every request goes through the safety transport.
type Client struct {
	Clientset      kubernetes.Interface
	Recorder       *safety.Recorder
	Namespace      string
	OtherNamespace string
	Server         string
	Version        string
	Identity       report.Identity
	// NamespaceObject is the target namespace read once at startup; NamespaceErr is the error
	// that read returned (a 403 is kept for AT-09).
	NamespaceObject *corev1.Namespace
	NamespaceErr    error

	firstSSAR   *authzv1.SelfSubjectAccessReview // startup answer, reused once by AT-01
	firstErr    error                            // ErrNoVerdict from startup's question, handed to AT-01 the same way
	quotas      func() ([]corev1.ResourceQuota, error)
	limitRanges func() ([]corev1.LimitRange, error)
}

// Connect builds the client and runs the startup sequence. It returns when startup is done or
// when ctx ends, so a hung credential plugin or a silent server cannot stall the run.
func Connect(ctx context.Context, cfg *rest.Config, opts Options) (*Client, error) {
	type outcome struct {
		c   *Client
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		c, err := connect(ctx, cfg, opts)
		done <- outcome{c, err}
	}()
	select {
	case o := <-done:
		return o.c, o.err
	case <-ctx.Done():
		return nil, opts.ended(ctx)
	}
}

func connect(ctx context.Context, cfg *rest.Config, opts Options) (*Client, error) {
	cfg = rest.CopyConfig(cfg)
	cfg.QPS, cfg.Burst = 50, 300 // kubectl's values; client-go's defaults would throttle the run
	// JSON on the wire, so --debug can print the SSAR and probe answers as text.
	cfg.ContentType, cfg.AcceptContentTypes = runtime.ContentTypeJSON, runtime.ContentTypeJSON
	base, _, err := rest.DefaultServerUrlFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot load the kubeconfig: %w", err)
	}
	rec := cmp.Or(opts.Recorder, &safety.Recorder{})
	cfg.Wrap(safety.Wrap(opts.Namespace, base, rec))
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	c := &Client{Clientset: cs, Recorder: rec, Namespace: opts.Namespace, OtherNamespace: opts.OtherNamespace, Server: cfg.Host}
	c.quotas = sync.OnceValues(func() ([]corev1.ResourceQuota, error) {
		l, err := cs.CoreV1().ResourceQuotas(opts.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	})
	c.limitRanges = sync.OnceValues(func() ([]corev1.LimitRange, error) {
		l, err := cs.CoreV1().LimitRanges(opts.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	})
	if err := c.startup(ctx, opts); err != nil {
		return nil, err
	}
	return c, nil
}

// Quotas lists the namespace's ResourceQuotas once per run.
func (c *Client) Quotas() ([]corev1.ResourceQuota, error) { return c.quotas() }

// LimitRanges lists the namespace's LimitRanges once per run.
func (c *Client) LimitRanges() ([]corev1.LimitRange, error) { return c.limitRanges() }

// ErrNoVerdict means an SSAR answer has no status.allowed. The API server always sends that field,
// so something else answered, such as a proxy that reflects the request.
var ErrNoVerdict = errors.New("the answer has no verdict (status.allowed)")

// SelfSubjectAccessReview asks the cluster "may I?". The answer startup already fetched for
// FirstQuestion is handed out once instead of being asked again.
func (c *Client) SelfSubjectAccessReview(ctx context.Context, attrs Attributes) (*authzv1.SelfSubjectAccessReview, error) {
	if attrs == FirstQuestion && (c.firstSSAR != nil || c.firstErr != nil) {
		first, err := c.firstSSAR, c.firstErr
		c.firstSSAR, c.firstErr = nil, nil
		return first, err
	}
	ssar := &authzv1.SelfSubjectAccessReview{Spec: authzv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &attrs}}
	// Not the typed Create: a decoded bool cannot tell "allowed": false from a missing field.
	res := c.Clientset.AuthorizationV1().RESTClient().Post().Resource("selfsubjectaccessreviews").Body(ssar).Do(ctx)
	answer := &authzv1.SelfSubjectAccessReview{} // a fresh object, so a missing echo stays missing
	if err := res.Into(answer); err != nil {
		return nil, err
	}
	raw, _ := res.Raw()
	var verdict struct {
		Status struct {
			Allowed *bool `json:"allowed"`
		} `json:"status"`
	}
	if json.Unmarshal(raw, &verdict) != nil || verdict.Status.Allowed == nil {
		return nil, ErrNoVerdict
	}
	return answer, nil
}
