package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const JobberCG = "/sys/fs/cgroup/jobber"

type ArgMaker func(JobDescription) (string, []string)

type Job struct {
	ID     string
	Spec   JobSpec
	Status JobStatus

	argMaker ArgMaker

	mu  sync.Mutex
	cmd *exec.Cmd

	logFeeder *feeder

	reaped chan struct{}
	done   chan struct{}
}

type JobSpec struct {
	Command string   `arg:"" help:"Command for jobber server to run"`
	Args    []string `arg:"" optional:"" help:"Arguments to command"`

	Root           string `help:"run in isolated root directory"`
	IsolateNetwork bool   `help:"run in isolated network namespace"`

	Resources ResourceLimits `embed:""`
}

type ResourceLimits struct {
	MaxProcesses uint32         `help:"maximum number of processes"`
	Memory       uint64         `help:"maximum memory (bytes)"`
	CPU          uint32         `help:"maximum CPU (milliCPU)"`
	IO           []DiskIOLimits `name:"io" help:"disk io limits (dev:rbps:wbps:riops:wiops)"`
}

type JobState int

const (
	JobStatePreStart = iota
	JobStateRunning
	JobStateCompleted
)

type JobStatus struct {
	StartTime time.Time
	Owner     string
	State     JobState
	ExitCode  uint32
	ExitError error
}

type JobDescription struct {
	ID     string
	Spec   JobSpec
	Status JobStatus
}

var (
	ErrAlreadyStarted = errors.New("job already started")
)

func NewJob(id string, spec JobSpec, argMaker ArgMaker) *Job {
	return &Job{ID: id, Spec: spec, argMaker: argMaker}
}

// Start runs the job.
func (j *Job) Start(owner string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.Status.State != JobStatePreStart {
		return fmt.Errorf("%s: %w", j.ID, ErrAlreadyStarted)
	}

	j.Status.State = JobStateRunning
	j.Status.StartTime = time.Now()
	j.Status.Owner = owner

	output, err := j.ExecPart1()
	if err != nil {
		// j.Status.State = JobStateCompleted
		return err
	}

	// At this point, the job's command has successfully started, so we
	// will not return an error. A feeder will be attached to the job's
	// output stream and left to run until EOF/error, at which point it
	// will Wait on the process to collect its exit code.
	j.done = make(chan struct{})
	j.reaped = make(chan struct{})
	logchan := make(chan Log)
	go func() {
		infeed(output, logchan)

		j.mu.Lock()
		cmd := j.cmd
		j.mu.Unlock()

		err := cmd.Wait()

		j.mu.Lock()
		if exitErr, ok := err.(*exec.ExitError); ok {
			// XXX ExitCode() can return -1 if exited via a signal, which
			// is strange as it is meant to be 128+signum. Just mask it
			// to 255 for now and figure it out later.
			j.Status.ExitCode = uint32(exitErr.ExitCode()) & 0xFF
		}
		j.Status.ExitError = err
		j.Status.State = JobStateCompleted
		close(j.reaped)
		j.cleanupCgroup()
		j.mu.Unlock()
	}()
	j.logFeeder = newFeeder(logchan)
	go j.logFeeder.Start(j.done)
	return nil
}

// Stop terminates the job (with extreme prejudice - SIGKILL). The job
// lock must be held.
func (j *Job) Stop(ctx context.Context) {
	j.mu.Lock()

	// XXX No SIGTERM, No grace period
	_ = j.cmd.Process.Kill() // SIGKILL

	reaped := j.reaped
	// We need to release the job lock while we wait for it to be
	// reaped, as the reaper needs the lock to update the job's
	// status and exit code.
	j.mu.Unlock()

	// Wait for the job to be reaped or for the context to be cancelled.
	select {
	case <-reaped:
	case <-ctx.Done():
	}
}

func (j *Job) Description() JobDescription {
	j.mu.Lock()
	defer j.mu.Unlock()
	return JobDescription{ID: j.ID, Spec: j.Spec, Status: j.Status}
}

func (j *Job) AttachOutfeed(follow bool, done <-chan struct{}) <-chan Log {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.logFeeder.attachOutfeed(follow, done)
}

func (j *Job) Cleanup() {
	// lock not needed
	close(j.done)
}

// ExecPart1 starts the execution of a job's command, ensuring it runs in new
// namespaces where appropriate, attaching pipes to capture the output of the
// command and any errors that come from not being able to run the command.
// It uses an ArgMaker to construct the command line as we do not know anything
// about the program we are embedded in and what command line args it takes.
// The ArgMaker abstracts that for us and allows the user of this package to
// define how to propagate Job parameters into a Job for ExecPart2 in a child
// process.
//
// If successful, it returns an io.ReadCloser that can be read for the command's
// combined stdout/stderr stream. Once that has closed, Job.cmd.Wait() should be
// called on the job to capture the exit code of the process and reap it.
func (j *Job) ExecPart1() (io.ReadCloser, error) {
	cmd := &exec.Cmd{
		Stdin: nil, // /dev/null
		SysProcAttr: &syscall.SysProcAttr{
			Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
			Unshareflags: syscall.CLONE_NEWNS,
		},
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if j.Spec.IsolateNetwork {
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWNET
	}

	jd := JobDescription{ID: j.ID, Spec: j.Spec, Status: j.Status}
	cmd.Path, cmd.Args = j.argMaker(jd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Read from the stderr pipe. If we get io.EOF without reading anything
	// it means the command has successfully been executed. Otherwise something
	// failed and the command was not executed at all. The reason/error is
	// written to the stderr pipe.
	errmsg, err := io.ReadAll(stderr)
	if err != nil {
		// could not read stderr. oh o
		// XXX what does this mean and how do we need to handle it.
		j.cleanupCgroup()
		return nil, err
	}
	if len(errmsg) > 0 {
		j.cleanupCgroup()
		return nil, errors.New(string(errmsg))
	}

	j.cmd = cmd
	return stdout, nil
}

func (j *Job) cleanupCgroup() {
	// Remove the cgroup created for the job.
	// This is necessary as part 2 uses syscall.Exec so there is nothing
	// left from the process to clean this up.
	// XXX See how to do this automatically with CLONE_NEWCGROUP/CLONE_INTO_CGROUP
	// XXX Handle error somehow, which may not be an error if the child
	// never got to creating the cgroup.
	_ = syscall.Rmdir(filepath.Join(JobberCG, j.ID))
}

// ExecPart2 runs the job in a cgroup configured from the job's parameters
// and configures the namespaces it is already in. It is expected that the
// process is already running in "empty" namespaces based on the job's
// configuration.
//
// It is expected that the standard io streams are set up as follows:
// * stdin: /dev/null
// * stdout: where the process's stdout and stderr are sent
// * stderr: where error messages due to the inability to run the program
//   are sent - e.g. errors setting up the cgroup, being unable to exec
//   the program (not found), etc.
//
// When the command is executed, it will have the stderr stream it received
// closed and will instead have the stdout stream on stderr too.
//
// It does not return an error, instead writing errors to stderr to be
// captured by the parent process in ExecPart1().
func (j *Job) ExecPart2() {
	// We want to duplicate stderr to a new file descriptor so we can set
	// up the command to capture its stdout/stderr to the same stream.
	// The new file descriptor should be set up FD_CLOEXEC to close it when
	// the command is executed. This is annoyingly verbose. We can only
	errfd, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not dup stderr: %v", err)
		return
	}
	errFile := os.NewFile(uintptr(errfd), "err")

	// does not return error
	syscall.CloseOnExec(errfd)

	if err := syscall.Dup2(syscall.Stdout, syscall.Stderr); err != nil {
		fmt.Fprintf(errFile, "could not dup stdout: %v", err)
		return
	}

	if err := j.execPart2(); err != nil {
		fmt.Fprint(errFile, err)
	}
}

// execPart2 sets up the job's cgroup and namespaces and execs its command.
func (j *Job) execPart2() error {
	if err := newCgroup(j.ID); err != nil {
		return err
	}

	spec := j.Spec
	r := spec.Resources

	if r.MaxProcesses > 0 {
		err := cgWrite(j.ID, "pids.max", strconv.FormatUint(uint64(r.MaxProcesses), 10))
		if err != nil {
			return fmt.Errorf("could not set pids.max: %w", err)
		}
	}

	if r.Memory > 0 {
		err := cgWrite(j.ID, "memory.max", strconv.FormatUint(r.Memory, 10))
		if err != nil {
			return fmt.Errorf("could not set memory.max: %w", err)
		}
	}

	if r.CPU > 0 {
		// Units are in microseconds, so scale our milliCPUs to microCPUs
		// XXX Not sure this is right. Seems very bursty in practice.
		err := cgWrite(j.ID, "cpu.max", fmt.Sprintf("%d 1000000", r.CPU*1000))
		if err != nil {
			return fmt.Errorf("could not set cpu.max: %w", err)
		}
	}

	for _, iolim := range r.IO {
		err := cgWrite(j.ID, "io.max", iolim.cgval())
		if err != nil {
			return fmt.Errorf("could not set io.max: %s: %w", iolim.cgval(), err)
		}
	}

	if err := syscall.Sethostname([]byte(j.ID)); err != nil {
		return fmt.Errorf("could not set container hostname: %w", err)
	}

	if spec.Root != "" {
		if err := syscall.Chroot(spec.Root); err != nil {
			return fmt.Errorf("could not set root directory to %s: %w", spec.Root, err)
		}
	}

	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("could not change to root directory: %w", err)
	}
	if err := syscall.Mount("proc", "/proc", "proc", 0 /* flags */, "" /* data */); err != nil {
		return fmt.Errorf("could not mount /proc: %w", err)
	}

	argv := append([]string{filepath.Base(spec.Command)}, spec.Args...)
	err := syscall.Exec(spec.Command, argv, nil /* environ */)
	if err != nil {
		return fmt.Errorf("could not exec %s: %w", spec.Command, err)
	}

	// NOTREACHED
	return nil
}

func InitCgroups() error {
	err := os.Mkdir(JobberCG, 0755)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("could not create jobber cgroup: %w", err)
	}

	// XXX Not sure if cpuset is required.
	if err := cgWrite("cgroup.subtree_control", "", "+cpu +cpuset +io +memory +pids"); err != nil {
		return fmt.Errorf("could not configure cgroup controllers: %w", err)
	}
	return nil
}

func newCgroup(id string) error {
	jobCG := filepath.Join(JobberCG, id)
	err := os.Mkdir(jobCG, 0755)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("could not create job (%s) cgroup: %w", id, err)
	}

	if err := cgWrite(id, "cgroup.procs", strconv.Itoa(os.Getpid())); err != nil {
		return fmt.Errorf("could not put outselves into cgroup: %w", err)
	}

	return nil
}

func cgWrite(id, setting, value string) error {
	return os.WriteFile(filepath.Join(JobberCG, id, setting), []byte(value), 0700)
}
