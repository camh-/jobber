package service

import (
	"bytes"
	"context"
	"sort"

	"github.com/camh-/jobber/job"
	pb "github.com/camh-/jobber/pb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// XXX Default user until authentication is added
const authedUser = "eve"

type JobExecutor struct {
	pb.UnimplementedJobExecutorServer

	tracker *job.Tracker
}

func NewJobExecutor(argMaker job.ArgMaker) *JobExecutor {
	return &JobExecutor{
		tracker: job.NewTracker(argMaker),
	}
}

func (svc *JobExecutor) RegisterWith(gs grpc.ServiceRegistrar) {
	pb.RegisterJobExecutorServer(gs, svc)
}

func (svc *JobExecutor) Run(ctx context.Context, req *pb.RunRequest) (*pb.RunResponse, error) {
	spec, err := newJobSpec(req.GetSpec())
	if err != nil {
		return nil, err
	}
	ctx = job.AddUserToContext(ctx, authedUser) // XXX temporary
	id, err := svc.tracker.Start(ctx, spec)
	if err != nil {
		// XXX do gRPC status/errors properly
		return nil, err
	}
	return &pb.RunResponse{JobId: []byte(id)}, nil
}

func (svc *JobExecutor) Stop(ctx context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	// XXX authorization check
	ctx = job.AddUserToContext(ctx, authedUser) // XXX temporary
	if err := svc.tracker.Stop(ctx, string(req.GetJobId()), req.GetCleanup()); err != nil {
		// XXX do gRPC status/errors properly
		return nil, err
	}
	return &pb.StopResponse{}, nil
}

func (svc *JobExecutor) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	// XXX authorization check
	ctx = job.AddUserToContext(ctx, authedUser) // XXX temporary
	jd, err := svc.tracker.Get(ctx, string(req.GetJobId()))
	if err != nil {
		// XXX do gRPC status/errors properly
		return nil, err
	}
	return &pb.StatusResponse{Status: newJobStatusPB(jd)}, nil
}

func (svc *JobExecutor) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	// XXX authorization check
	user := authedUser
	if req.GetAllJobs() {
		// XXX assumes admin for now. temporary hack until we have admin
		// modelled in the tracker.
		user = ""
	}
	ctx = job.AddUserToContext(ctx, user) // XXX temporary

	resp := &pb.ListResponse{}
	for _, jd := range svc.tracker.List(ctx, req.GetCompleted()) {
		resp.Jobs = append(resp.Jobs, newJobStatusPB(jd))
	}

	// Sort jobs by start time, then job ID for a determinstic ordering
	sort.Slice(resp.Jobs, func(i, j int) bool {
		a, b := resp.Jobs[i], resp.Jobs[j]
		if !a.StartTime.AsTime().Equal(b.StartTime.AsTime()) {
			return a.StartTime.AsTime().Before(b.StartTime.AsTime())
		}
		return bytes.Compare(a.JobId, b.JobId) < 0
	})
	return resp, nil
}

func (svc *JobExecutor) Logs(req *pb.LogsRequest, stream pb.JobExecutor_LogsServer) error {
	// XXX authorization check

	id, follow, ctx := string(req.GetJobId()), req.GetFollow(), stream.Context()
	ctx = job.AddUserToContext(ctx, authedUser) // XXX temporary
	ch, err := svc.tracker.GetLogChannel(id, follow, ctx)
	if err != nil {
		return err
	}

	for l := range ch {
		resp := pb.LogsResponse{
			Line:      []byte(l.Line),
			Timestamp: timestamppb.New(l.Timestamp),
		}
		if err := stream.Send(&resp); err != nil {
			return err
		}
	}
	return nil
}

// Convert a protobuf JobSpec to a job.JobSpec
func newJobSpec(pbspec *pb.JobSpec) (job.JobSpec, error) {
	pbresources := pbspec.GetResources()
	var iolimits []job.DiskIOLimits
	for _, pblim := range pbresources.GetIoLimits() {
		iolim := job.DiskIOLimits{
			Device:    pblim.Device,
			ReadBPS:   pblim.ReadBps,
			WriteBPS:  pblim.WriteBps,
			ReadIOPS:  pblim.ReadIops,
			WriteIOPS: pblim.ReadIops,
		}
		if err := iolim.ResolveDevice(); err != nil {
			return job.JobSpec{}, err
		}
		iolimits = append(iolimits, iolim)
	}

	return job.JobSpec{
		Command:        pbspec.GetCommand(),
		Args:           pbspec.GetArguments(),
		Root:           pbspec.GetRootDir(),
		IsolateNetwork: pbspec.GetIsolateNetwork(),
		Resources: job.ResourceLimits{
			MaxProcesses: pbresources.GetMaxProcesses(),
			Memory:       pbresources.GetMemory(),
			CPU:          pbresources.GetMilliCpu(),
			IO:           iolimits,
		},
	}, nil
}

// Create a protobuf JobStatus from a job.Job
func newJobStatusPB(jd job.JobDescription) *pb.JobStatus {
	var state pb.JobStatus_JobState
	switch jd.Status.State {
	case job.JobStatePreStart:
		// nothing. leave as invalid. this should never appear in a tracker
		// XXX maybe panic?
	case job.JobStateRunning:
		state = pb.JobStatus_JOBSTATE_RUNNING
	case job.JobStateCompleted:
		state = pb.JobStatus_JOBSTATE_COMPLETED
	default:
		// leave as invalid
	}

	return &pb.JobStatus{
		JobId:     []byte(jd.ID),
		StartTime: timestamppb.New(jd.Status.StartTime),
		User:      jd.Status.Owner,
		State:     state,
		ExitCode:  jd.Status.ExitCode,
		Spec:      nil, // XXX todo. nothing uses it yet
	}
}
