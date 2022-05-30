package cli

import (
	"strconv"

	"github.com/camh-/jobber/job"
)

// ProcSelfArgMaker returns a command line to run the job in a container.
// This runs ourself as "/proc/self/exe rc ..."
func ProcSelfArgMaker(j *job.Job) (cmd string, args []string) {
	argv := []string{"--id", j.ID}

	spec := j.Spec
	r := spec.Resources

	if spec.Root != "" {
		argv = append(argv, "--root", spec.Root)
	}
	if spec.IsolateNetwork {
		argv = append(argv, "--isolate-network")
	}
	if r.MaxProcesses != 0 {
		argv = append(argv, "--max-processes", strconv.FormatUint(uint64(r.MaxProcesses), 10))
	}
	if r.Memory != 0 {
		argv = append(argv, "--memory", strconv.FormatUint(r.Memory, 10))
	}
	if r.CPU != 0 {
		argv = append(argv, "--cpu", strconv.FormatUint(uint64(r.CPU), 10))
	}
	for _, iolim := range r.IO {
		argv = append(argv, "--io", iolim.String())
	}

	argv = append(argv, "--", spec.Command)
	argv = append(argv, spec.Args...)

	return "/proc/self/exe", append([]string{"jobber", "rc"}, argv...)
}
