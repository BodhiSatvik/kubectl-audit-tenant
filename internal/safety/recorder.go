package safety

import (
	"slices"
	"sync"
	"time"
)

// Request is one API request that passed the allowlist. It never holds request headers.
type Request struct {
	Method   string
	Path     string // path and query
	Status   int    // 0 when no response came back
	Duration time.Duration
	Body     string // POST answers: SSARs whole, SelfSubjectReview without userInfo.extra, create rejections whole, accepted creates as createSummary (D140, D145); empty for GET
}

// Recorder keeps every request of a run for --debug and the request budget.
type Recorder struct {
	mu   sync.Mutex
	reqs []Request
}

func (r *Recorder) add(req Request) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
}

// Requests returns the recorded requests in order.
func (r *Recorder) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.reqs)
}
