package auth

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"holdings/backend/internal/database"
)

// jobWaitMax is how long a long-poll request waits for a change. The server's
// WriteTimeout is 15 s (cmd/api/main.go), so the answer is always written in time.
const jobWaitMax = 10 * time.Second

// touch records a change of job and wakes everyone waiting for it. The caller
// holds j.mu. Waiters take their channel under the same lock that guards the
// version they compared, so a change cannot slip in between check and wait.
func (j *priceJobs) touch(job *priceJob) {
	job.Version++
	if job.changed != nil {
		close(job.changed)
		job.changed = nil
	}
}

// changedChan returns the channel that touch will close. The caller holds j.mu.
func (j *priceJobs) changedChan(job *priceJob) <-chan struct{} {
	if job.changed == nil {
		job.changed = make(chan struct{})
	}
	return job.changed
}

// snapshot copies a job for encoding outside the lock. The caller holds j.mu.
func (job *priceJob) snapshot() priceJob {
	s := *job
	s.Items = append([]priceItem(nil), job.Items...)
	s.changed = nil
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
// job is DONE; the wait also ends when the client goes away or the handler closes,
// and then the current state is returned. No lock is held while waiting.
func (h *Handler) awaitJob(ctx context.Context, user, id string, since int64, long bool) (priceJob, bool) {
	var timeout <-chan time.Time
	for {
		h.jobs.mu.Lock()
		j := h.jobs.entries[id]
		if j == nil || j.user != user || (j.Status == "DONE" && time.Since(j.created) > time.Hour) {
			h.jobs.mu.Unlock()
			return priceJob{}, false
		}
		if !long || j.Version > since || j.Status == "DONE" {
			snap := j.snapshot()
			h.jobs.mu.Unlock()
			return snap, true
		}
		changed := h.jobs.changedChan(j)
		h.jobs.mu.Unlock()
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
