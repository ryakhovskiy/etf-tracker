package auth

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"holdings/backend/internal/database"
)

// jobWaitMax is how long a long-poll request waits for a change. The server's
// WriteTimeout is 15 s (cmd/api/main.go), so the answer is always written in time.
const jobWaitMax = 10 * time.Second

// jobSignal is the wake-up half of a long-polled job (price and dividend jobs
// share it). All of its methods are called with the lock that guards the job's
// version held. A waiter takes its channel under the same lock that guards the
// version it compared, so a change cannot slip in between check and wait.
type jobSignal struct{ ch chan struct{} }

// wake releases everyone waiting for the next change.
func (s *jobSignal) wake() {
	if s.ch != nil {
		close(s.ch)
		s.ch = nil
	}
}

// wait returns the channel that the next wake closes.
func (s *jobSignal) wait() <-chan struct{} {
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}

// touch records a change of job and wakes everyone waiting for it. The caller
// holds j.mu.
func (j *priceJobs) touch(job *priceJob) {
	job.Version++
	job.changed.wake()
}

// snapshot copies a job for encoding outside the lock. The caller holds j.mu.
func (job *priceJob) snapshot() priceJob {
	s := *job
	s.Items = make([]priceItem, len(job.Items)) // never nil: it encodes as []
	copy(s.Items, job.Items)
	s.changed = jobSignal{}
	return s
}

func (h *Handler) jobWaitLimit() time.Duration {
	if h.jobWait > 0 {
		return h.jobWait
	}
	return jobWaitMax
}

// parseSince reads the optional ?since=<version> of a job request.
func parseSince(r *http.Request) (since int64, long bool, err error) {
	raw, present := r.URL.Query()["since"]
	if !present {
		return 0, false, nil
	}
	since, err = strconv.ParseInt(raw[0], 10, 64)
	if err != nil || since < 0 {
		return 0, false, &database.ValidationError{Code: "VALIDATION_ERROR", Message: "since must be a non-negative job version.", Fields: map[string]string{"since": "Use the version of the last answer."}}
	}
	return since, true, nil
}

// awaitJob returns a snapshot of the user's job, or false when there is none
// (unknown, someone else's, or finished more than an hour ago). With long set it
// first waits, at most jobWaitLimit, until the job's version exceeds since or the
// job is DONE; see awaitChange.
func (h *Handler) awaitJob(ctx context.Context, user, id string, since int64, long bool) (priceJob, bool) {
	var snap priceJob
	found := h.awaitChange(ctx, &h.jobs.mu, long, func(long bool) (found, ready bool, wait <-chan struct{}) {
		j := h.jobs.entries[id]
		if j == nil || j.user != user || (j.Status == "DONE" && time.Since(j.created) > time.Hour) {
			return false, true, nil
		}
		if !long || j.Version > since || j.Status == "DONE" {
			snap = j.snapshot()
			return true, true, nil
		}
		return true, false, j.changed.wait()
	})
	return snap, found
}

// awaitChange is the long-poll loop shared by the job kinds. check runs with mu
// held and says whether the job exists (found), whether the answer is ready (ready),
// and otherwise returns the channel that the job's next change closes; when ready
// it has captured what to answer. While not ready the loop waits, holding no lock,
// at most jobWaitLimit; the wait also ends when the client goes away or the handler
// drains or closes, and then check runs once more with long=false so the current
// state is the answer.
func (h *Handler) awaitChange(ctx context.Context, mu *sync.Mutex, long bool, check func(long bool) (found, ready bool, wait <-chan struct{})) bool {
	var timeout <-chan time.Time
	for {
		mu.Lock()
		found, ready, changed := check(long)
		mu.Unlock()
		if ready {
			return found
		}
		if timeout == nil {
			timer := time.NewTimer(h.jobWaitLimit())
			defer timer.Stop()
			timeout = timer.C
		}
		select {
		case <-changed:
			continue // re-check under the lock: the version may have moved more than once
		case <-timeout:
		case <-ctx.Done():
		case <-h.drain.Done():
		case <-h.ctx.Done():
		}
		long = false // time is up: answer with the current state
	}
}
