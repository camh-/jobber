package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/camh-/jobber/job"
	pb "github.com/camh-/jobber/pb"
	"google.golang.org/grpc"
)

// client is a struct intended to be embedded in each of the client kong
// subcommand structs and provides common options for all client commands,
// as well as some methods for using those options.
type clientCmd struct {
	Address string `short:"A" default:"localhost:8443" env:"JOBBER_SERVER" help:"TCP address of jobber server"`

	TLSCert string `name:"tls-cert" default:"certs/user.crt" help:"TLS user cert"`
	TLSKey  string `name:"tls-key" default:"certs/user.key" help:"TLS user key"`
	CACert  string `name:"ca-cert" default:"certs/ca.crt" help:"CA for authenticating server"`

	conn   *grpc.ClientConn
	output io.Writer
}

// CmdRun is a kong struct describing the flags and arguments for the
// `jobber run` subcommand.
type CmdRun struct {
	clientCmd
	Detach       bool `short:"d" help:"Detach from output when running" xor:"ts"`
	NoTimestamps bool `short:"T" help:"Do not output timestamps on lines" xor:"ts"`

	job.JobSpec
}

// CmdStop is a kong struct describing the flags and arguments for the
// `jobber stop` subcommand.
type CmdStop struct {
	clientCmd
	Cleanup bool   `short:"c" help:"Remove job from jobber server after stopping. Can be used on already stopped job"`
	JobID   string `arg:"" help:"ID of job to stop"`
}

// CmdStatus is a kong struct describing the flags and arguments for the
// `jobber status` subcommand.
type CmdStatus struct {
	clientCmd
	JobID string `arg:"" help:"ID of job to get status of"`
}

// CmdList is a kong struct describing the flags and arguments for the
// `jobber list` subcommand.
type CmdList struct {
	clientCmd
	All       bool `short:"a" help:"List all user's jobs"`
	Completed bool `short:"c" help:"List completed as well as running jobs"`
}

// CmdLogs is a kong struct describing the flags and arguments for the
// `jobber logs` subcommand.
type CmdLogs struct {
	clientCmd
	Follow       bool   `short:"f" help:"Stream logs continuously as they are produced"`
	NoTimestamps bool   `short:"T" help:"Do not output timestamps on lines"`
	JobID        string `arg:"" help:"ID of job to fetch logs from"`
}

func (c *clientCmd) connect() (pb.JobExecutorClient, error) {
	creds, err := mTLSCreds(c.TLSCert, c.TLSKey, c.CACert)
	if err != nil {
		return nil, err
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	cc, err := grpc.Dial(c.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot dial %s: %w", c.Address, err)
	}

	c.conn = cc
	return pb.NewJobExecutorClient(cc), nil
}

func (c *clientCmd) writer() io.Writer {
	if c.output != nil {
		return c.output
	}
	return os.Stdout
}

func (c *clientCmd) Close() error {
	return c.conn.Close()
}

// Run is the entrypoint for the `jobber run` cli command. It packages the
// command line arguments into a `RunRequest` message and calls the
// `JobExecutor.Run()` method. If the detach flag is not specified, it
// calls the `JobExecutor.Logs()` method after a successful run to stream
// back the logs of the run command.
//
// It is called by kong after parsing the command line.
func (cmd *CmdRun) Run() error {
	cl, err := cmd.connect()
	if err != nil {
		return err
	}
	defer cmd.Close()

	var iolims []*pb.DiskIOLimit
	for _, iolim := range cmd.Resources.IO {
		pblim := &pb.DiskIOLimit{
			Device:    iolim.Device,
			ReadBps:   iolim.ReadBPS,
			WriteBps:  iolim.WriteBPS,
			ReadIops:  iolim.ReadIOPS,
			WriteIops: iolim.WriteIOPS,
		}
		iolims = append(iolims, pblim)
	}

	req := pb.RunRequest{
		Spec: &pb.JobSpec{
			Command:        cmd.Command,
			Arguments:      cmd.Args,
			RootDir:        cmd.Root,
			IsolateNetwork: cmd.IsolateNetwork,
			Resources: &pb.Resources{
				MaxProcesses: cmd.Resources.MaxProcesses,
				MilliCpu:     cmd.Resources.CPU,
				Memory:       cmd.Resources.Memory,
				IoLimits:     iolims,
			},
		},
	}

	resp, err := cl.Run(context.Background(), &req)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.writer(), "job id:", string(resp.GetJobId()))

	if !cmd.Detach {
		return getLogs(cmd.writer(), cl, resp.GetJobId(), true /* follow */, !cmd.NoTimestamps)
	}

	return nil
}

// Run is the entrypoint for the `jobber stop` cli command. It packages the
// command line arguments into a `StopRequest` message and calls the
// `JobExecutor.Stop()` method.
//
// It is called by kong after parsing the command line.
func (cmd *CmdStop) Run() error {
	cl, err := cmd.connect()
	if err != nil {
		return err
	}
	defer cmd.Close()

	req := pb.StopRequest{
		JobId:   []byte(cmd.JobID),
		Cleanup: cmd.Cleanup,
	}

	_, err = cl.Stop(context.Background(), &req)
	return err
}

// Run is the entrypoint for the `jobber status` cli command. It packages the
// command line arguments into a `StatusRequest` message and calls the
// `JobExecutor.Status()` method.
//
// It is called by kong after parsing the command line.
func (cmd *CmdStatus) Run() error {
	cl, err := cmd.connect()
	if err != nil {
		return err
	}
	defer cmd.Close()

	req := pb.StatusRequest{
		JobId: []byte(cmd.JobID),
	}

	resp, err := cl.Status(context.Background(), &req)
	if err != nil {
		return err
	}

	return printStatus(cmd.writer(), resp.GetStatus())
}

// Run is the entrypoint for the `jobber list` cli command. It packages the
// command line arguments into a `ListRequest` message and calls the
// `JobExecutor.List()` method.
//
// It is called by kong after parsing the command line.
func (cmd *CmdList) Run() error {
	cl, err := cmd.connect()
	if err != nil {
		return err
	}
	defer cmd.Close()

	req := pb.ListRequest{AllJobs: cmd.All, Completed: cmd.Completed}
	resp, err := cl.List(context.Background(), &req)
	if err != nil {
		return err
	}

	return printStatus(cmd.writer(), resp.GetJobs()...)
}

// Run is the entrypoint for the `jobber logs` cli command. It packages the
// command line arguments into a `LogsRequest` message and calls the
// `JobExecutor.Logs()` method.
//
// It is called by kong after parsing the command line.
func (cmd *CmdLogs) Run() error {
	cl, err := cmd.connect()
	if err != nil {
		return err
	}
	defer cmd.Close()

	return getLogs(cmd.writer(), cl, []byte(cmd.JobID), cmd.Follow, !cmd.NoTimestamps)
}

// printStatus formats the JobStatuses passed to it and writes them to the
// given io.Writer. It writes one job status per line, with a header.
func printStatus(w io.Writer, statuses ...*pb.JobStatus) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB ID\tSTART TIME\tUSER\tSTATUS")

	for _, status := range statuses {
		state := "unknown"
		switch status.GetState() {
		case pb.JobStatus_JOBSTATE_RUNNING:
			state = "running"
		case pb.JobStatus_JOBSTATE_COMPLETED:
			state = fmt.Sprintf("exited (%d)", status.GetExitCode())
		}

		ts := status.GetStartTime().AsTime().Format(time.Stamp)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", status.GetJobId(), ts, status.GetUser(), state)
	}
	return tw.Flush()
}

// getLogs performs a `JobExecutor.Logs()` method call for a job and writes
// the logs streamed back to the given io.Writer. If follow is true, it will
// continue to stream logs while the job continues to run. If showTimestamp
// is true the log timestamp is printed before each log line.
func getLogs(w io.Writer, cl pb.JobExecutorClient, id []byte, follow bool, showTimestamp bool) error {
	logsReq := pb.LogsRequest{
		JobId:  id,
		Follow: follow,
	}
	stream, err := cl.Logs(context.Background(), &logsReq)
	if err != nil {
		return err
	}

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if showTimestamp {
			fmt.Print(resp.Timestamp.AsTime().Format(time.RFC3339), " ")
		}
		fmt.Fprint(w, string(resp.Line))
		if l := len(resp.Line); showTimestamp && l > 0 && resp.Line[l-1] != '\n' {
			// Add a newline on lines without a newline only if we are
			// prefixing timestamps.
			fmt.Fprintln(w)
		}
	}

	return nil
}
