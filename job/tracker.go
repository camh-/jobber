package job

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"sync"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrMissingID    = errors.New("missing job ID")
	ErrNoCommand    = errors.New("missing job command")
	ErrNotStarted   = errors.New("could not start job")
	ErrUnknown      = errors.New("unknown job")
)

// Tracker maintains a set of Jobs that are either running or have completed.
// Jobs can be added (started), stopped (including removed via cleanup if
// desired), listed and attached to for log output.
type Tracker struct {
	jobs   map[string]*Job
	mu     sync.Mutex
	admins map[string]bool

	argMaker ArgMaker
}

func NewTracker(argMaker ArgMaker, admins []string) *Tracker {
	t := &Tracker{
		jobs:     make(map[string]*Job),
		admins:   make(map[string]bool),
		argMaker: argMaker,
	}
	for _, admin := range admins {
		t.admins[admin] = true
	}
	return t
}

type userContextKey struct{}

func AddUserToContext(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

func GetUserFromContext(ctx context.Context) (string, bool) {
	u, ok := ctx.Value(userContextKey{}).(string)
	return u, ok
}

// Start runs the given job. If it starts, the job will be tracked and can be
// operated upon. If it does not start, an error is returned and the job is
// not tracked.
func (t *Tracker) Start(ctx context.Context, spec JobSpec) (string, error) {
	user, ok := GetUserFromContext(ctx)
	if !ok {
		return "", ErrUnauthorized
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if spec.Command == "" {
		return "", ErrNoCommand
	}

	id := t.allocateID(spec)
	j := NewJob(id, spec, t.argMaker)

	if err := j.Start(user); err != nil {
		// don't track a job we can't start
		return "", fmt.Errorf("%w: %v", ErrNotStarted, err) // would be nice to wrap both
	}
	t.jobs[id] = j

	return id, nil
}

// Stop kills the job identified by id. It waits until the job exits before
// returning, unless the context is cancelled.
func (t *Tracker) Stop(ctx context.Context, id string, cleanup bool) error {
	user, ok := GetUserFromContext(ctx)
	if !ok {
		return ErrUnauthorized
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	j, ok := t.jobs[id]
	if !ok {
		return fmt.Errorf("%s: %w", id, ErrUnknown)
	}

	jd := j.Description()

	if jd.Status.Owner != user && !t.admins[user] {
		// XXX should probably be ErrUnknown to avoid enumeration attacks
		return ErrUnauthorized
	}

	if jd.Status.State == JobStateRunning {
		j.Stop(ctx)
	}

	if cleanup {
		j.Cleanup()
		delete(t.jobs, id)
	}

	return nil
}

// Get returns a copy of the job identified by id if it exists in the tracker,
// otherwise an error. The copy returned is not an active job that can be
// manipulated - it is just for the data.
func (t *Tracker) Get(ctx context.Context, id string) (JobDescription, error) {
	user, ok := GetUserFromContext(ctx)
	if !ok {
		return JobDescription{}, ErrUnauthorized
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	j, ok := t.jobs[id]
	if !ok {
		return JobDescription{}, fmt.Errorf("%s: %w", id, ErrUnknown)
	}

	jd := j.Description()

	if jd.Status.Owner != user && !t.admins[user] {
		// XXX should probably be ErrUnknown to avoid enumeration attacks
		return JobDescription{}, ErrUnauthorized
	}

	return jd, nil

}

// List returns a copy of all the jobs for a owner, or all jobs if the given
// owner is empty. Only running jobs are returned, unless completed is true.
func (t *Tracker) List(ctx context.Context, completed, all bool) []JobDescription {
	user, ok := GetUserFromContext(ctx)
	if !ok {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var jobs []JobDescription
	for _, j := range t.jobs {
		// XXX maybe clean up locking by using a function in the loop body
		jd := j.Description()
		if user != jd.Status.Owner && !(all && t.admins[user]) {
			continue
		}
		if !completed && jd.Status.State == JobStateCompleted {
			continue
		}
		jobs = append(jobs, jd)
	}

	return jobs
}

// GetLogChannel returns a channel that streams the logs of the job identified
// by id. If follow is set, the stream will continue until the job terminates.
// Regardless of the follow flag, if the context is closed, then the
// returned log channel is detached from the log feeder and is closed.
func (t *Tracker) GetLogChannel(id string, follow bool, ctx context.Context) (<-chan Log, error) {
	user, ok := GetUserFromContext(ctx)
	if !ok {
		return nil, ErrUnauthorized
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	j, ok := t.jobs[id]
	if !ok {
		return nil, fmt.Errorf("%s: %w", id, ErrUnknown)
	}

	jd := j.Description()

	if jd.Status.Owner != user && !t.admins[user] {
		// XXX should probably be ErrUnknown to avoid enumeration attacks
		return nil, ErrUnauthorized
	}

	return j.AttachOutfeed(follow, ctx.Done()), nil
}

func (t *Tracker) allocateID(spec JobSpec) string {
	// XXX If we have 4 billion jobs with the same command, this could loop
	// infinitely. A good program would check that :(
	for {
		// pseudo-randomness is good enough for this.
		base := filepath.Base(spec.Command) + "-"
		id := base + strconv.FormatUint(uint64(rand.Uint32()), 16)
		if _, ok := t.jobs[id]; !ok {
			return id
		}
	}
}
