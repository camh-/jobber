package cli

import (
	"net"

	"github.com/camh-/jobber/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// CmdServe is a kong struct describing the flags and arguments for the
// `jobber serve` subcommand.
type CmdServe struct {
	Listen string `short:"l" default:":8080" help:"TCP listen address"`
}

// Run is the entrypoint for the `jobber serve` cli command. It starts a
// grpc server and serves a fake implementation of the JobExecutor service.
// gRPC server reflection is enabled on the gRPC server.
func (cmd *CmdServe) Run() error {
	l, err := net.Listen("tcp", cmd.Listen)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer()

	jobberService := service.NewFake()
	jobberService.RegisterWith(grpcServer)

	reflection.Register(grpcServer)

	// grpcServer takes ownership of l (net.Listen)
	return grpcServer.Serve(l)
}
