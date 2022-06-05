package main

import (
	"github.com/alecthomas/kong"
	"github.com/camh-/jobber/cli"
)

// version will be set by a go linker flag when a release build is made
var version = "v0.0.0"

// config is the top level of the command line parse tree. Flags common to all
// commands go here as well as the subcommands that the program provides.
type config struct {
	Version kong.VersionFlag `short:"V" help:"Print version information"`

	// Server commands
	Serve    cli.CmdServe        `cmd:"" help:"Serve the JobExecutor gRPC service"`
	Shutdown cli.CmdShutdown     `cmd:"" help:"kill all jobs and shutdown server"`
	Rc       cli.CmdRunContainer `cmd:"" hidden:""`
	Rj       cli.CmdRunJob       `cmd:"" hidden:""`

	// Client commands
	Run    cli.CmdRun    `cmd:"" help:"Run a job on a remote jobber server"`
	Stop   cli.CmdStop   `cmd:"" help:"Stop a job on a remote jobber server"`
	Status cli.CmdStatus `cmd:"" help:"Get status of a job on a remote jobber server"`
	List   cli.CmdList   `cmd:"" help:"List jobs on a remote jobber server"`
	Logs   cli.CmdLogs   `cmd:"" help:"Get logs (output) of job on remote jobber server"`
}

func main() {
	cli := &config{}
	kctx := kong.Parse(cli, kong.Vars{"version": version})

	// kctx.Run() will dispatch to the Run method of whichever subcommand
	// is on the command line.
	err := kctx.Run()
	kctx.FatalIfErrorf(err)
}
