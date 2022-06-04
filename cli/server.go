package cli

import (
	"fmt"
	"net"

	"github.com/camh-/jobber/job"
	"github.com/camh-/jobber/service"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// CmdServe is a kong struct describing the flags and arguments for the
// `jobber serve` subcommand.
type CmdServe struct {
	Listen string   `short:"l" default:":8443" help:"TCP listen address"`
	Admin  []string `help:"admin users with full privileges"`

	TLSCert string `name:"tls-cert" default:"certs/server.crt" help:"TLS server cert"`
	TLSKey  string `name:"tls-key" default:"certs/server.key" help:"TLS server key"`
	CACert  string `name:"ca-cert" default:"certs/ca.crt" help:"CA for authenticating users"`
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

	creds, err := mTLSCreds(cmd.TLSCert, cmd.TLSKey, cmd.CACert)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(creds),
		grpc.UnaryInterceptor(grpc_auth.UnaryServerInterceptor(CNToUser)),
		grpc.StreamInterceptor(grpc_auth.StreamServerInterceptor(CNToUser)),
	)

	jobberService := service.NewJobExecutor(ProcSelfArgMaker, cmd.Admin)
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
