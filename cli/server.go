package cli

import (
	"fmt"
	"net"

	"github.com/camh-/jobber/job"
	"github.com/camh-/jobber/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// CmdServe is a kong struct describing the flags and arguments for the
// `jobber serve` subcommand.
type CmdServe struct {
	Listen string `short:"l" default:":8080" help:"TCP listen address"`
}

// CmdRunJob is a hidden entrypoint just for testing the container runner
// without needing the while jobber server. It is not intended to be user-
// facing.
type CmdRunJob struct {
	job.JobSpec
	ID string `required:"" help:"job id"`
}

// CmdRunContainer is a hidden entrypoint for the jobber server to be able
// to run a child process in the correct namespaces so it can first set
// up those namespaces and cgroups.
type CmdRunContainer struct {
	job.JobSpec
	ID string `required:"" help:"job id"`
}

// Run is the entrypoint for the `jobber serve` cli command. It starts a
// grpc server and serves a fake implementation of the JobExecutor service.
// gRPC server reflection is enabled on the gRPC server.
func (cmd *CmdServe) Run() error {
	if err := job.InitCgroups(); err != nil {
		return err
	}

	l, err := net.Listen("tcp", cmd.Listen)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer()

	jobberService := service.NewJobExecutor(ProcSelfArgMaker)
	jobberService.RegisterWith(grpcServer)

	reflection.Register(grpcServer)

	// grpcServer takes ownership of l (net.Listen)
	return grpcServer.Serve(l)
}

// CmdRunJob is an internal command for directly running a container. It is
// not part of the server proper. It is for development testing only.
func (cmd *CmdRunJob) Run() error {
	if err := job.InitCgroups(); err != nil {
		return err
	}

	j := job.NewJob(cmd.ID, cmd.JobSpec, ProcSelfArgMaker)
	if err := j.Start("owner"); err != nil {
		return err
	}
	for l := range j.AttachOutfeed(true /* follow */, nil) {
		fmt.Print(string(l.Line))
	}
	return j.Status.ExitError
}

// CmdRunContainer implements `jobber rc` to execute part 2 of the
// container running process - setting up the cgroup(s) and namespace(s)
// and execing the job's command.
func (cmd *CmdRunContainer) Run() error {
	j := job.NewJob(cmd.ID, cmd.JobSpec, nil)
	j.ExecPart2()
	return nil
}
