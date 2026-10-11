package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	authnv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// FirstQuestion is AT-01's first SelfSubjectAccessReview; startup sends it to learn whether the
// identity may ask "may I?" at all.
var FirstQuestion = Attributes{Verb: "list", Resource: "nodes"}

// startup runs the four startup requests of DESIGN.md section 6 and fills the client.
func (c *Client) startup(ctx context.Context, opts Options) error {
	// Error() decodes the server's Status body; Raw() alone would report "unknown" on a 401 or 403.
	res := c.Clientset.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx)
	if err := res.Error(); err != nil {
		if fatal := opts.fatal(ctx, err); fatal != nil {
			return fatal
		}
		if apierrors.IsForbidden(err) {
			return fmt.Errorf("the API server refused /version for this identity: %w", err)
		}
		return fmt.Errorf("API server unreachable: %w", err)
	}
	raw, _ := res.Raw()
	var info version.Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("API server unreachable: /version returned no version: %w", err)
	}
	c.Version = info.GitVersion

	c.Identity = report.Identity{Mode: report.ModeSelf, Username: opts.User}
	if opts.As != "" {
		c.Identity = report.Identity{Mode: report.ModeImpersonation, Username: opts.As, Groups: opts.AsGroups}
	}
	review, err := c.Clientset.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	switch {
	case err == nil:
		c.Identity.Username = review.Status.UserInfo.Username
		c.Identity.Groups = review.Status.UserInfo.Groups
		c.Identity.Verified = true
	case opts.fatal(ctx, err) != nil:
		return opts.fatal(ctx, err)
	}
	// Any other failure (403, 404 on old servers) leaves the identity unverified.

	c.NamespaceObject, c.NamespaceErr = c.Clientset.CoreV1().Namespaces().Get(ctx, opts.Namespace, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(c.NamespaceErr):
		return fmt.Errorf("namespace %s not found in context %s; check -n or --context", opts.Namespace, opts.Context)
	case opts.fatal(ctx, c.NamespaceErr) != nil:
		return opts.fatal(ctx, c.NamespaceErr)
	}
	if c.NamespaceErr != nil {
		c.NamespaceObject = nil // a 403 or another error is kept for AT-09
	}

	c.firstSSAR, err = c.SelfSubjectAccessReview(ctx, FirstQuestion)
	switch {
	case err == nil:
		return nil
	case opts.fatal(ctx, err) != nil:
		return opts.fatal(ctx, err)
	case apierrors.IsForbidden(err):
		return fmt.Errorf("cannot ask the cluster \"may I?\": %w; hint: this identity lacks `create selfsubjectaccessreviews`, which every authenticated user normally has", err)
	}
	return fmt.Errorf("the first SelfSubjectAccessReview failed: %w", err)
}

// fatal maps a startup error to one of the exit-2 reasons that end the run, or nil.
func (o Options) fatal(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return o.ended(ctx)
	case apierrors.IsUnauthorized(err):
		return errors.New("unauthorized: the cluster rejected your credentials (expired token?); log in again and retry")
	case apierrors.IsForbidden(err) && strings.Contains(err.Error(), "cannot impersonate"):
		return fmt.Errorf("%s may not impersonate %s: ask a cluster admin for `impersonate` on users, groups and serviceaccounts, or run without --as", impersonator(err.Error()), o.As)
	}
	return nil
}

// ended explains why the run context ended during startup.
func (o Options) ended(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return errors.New("interrupted during startup; the cluster was not changed")
	}
	return fmt.Errorf("timed out during startup: the credential plugin or the API server did not answer within %s", o.Timeout)
}

// impersonator pulls the caller's name out of `User "x" cannot impersonate ...`.
func impersonator(msg string) string {
	if _, rest, ok := strings.Cut(msg, `User "`); ok {
		if name, _, ok := strings.Cut(rest, `"`); ok {
			return name
		}
	}
	return "your identity"
}
