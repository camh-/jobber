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

type JobExecutor struct {
	pb.UnimplementedJobExecutorServer

	tracker *job.Tracker
	done    chan<- struct{}
}

func NewJobExecutor(done chan<- struct{}, argMaker job.ArgMaker, admins []string) *JobExecutor {
	return &JobExecutor{
		tracker: job.NewTracker(argMaker, admins),
		done:    done,
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
	id, err := svc.tracker.Start(ctx, spec)
	if err != nil {
		// XXX do gRPC status/errors properly
		return nil, err
	}
	return &pb.RunResponse{JobId: []byte(id)}, nil
}

func (svc *JobExecutor) Stop(ctx context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	if err := svc.tracker.Stop(ctx, string(req.GetJobId()), req.GetCleanup()); err != nil {
		// XXX do gRPC status/errors properly
		return nil, err
	}
	return &pb.StopResponse{}, nil
}

func (svc *JobExecutor) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	jd, err := svc.tracker.Get(ctx, string(req.GetJobId()))
	if err != nil {
		// XXX do gRPC status/errors properly
		return nil, err
	}
	return &pb.StatusResponse{Status: newJobStatusPB(jd)}, nil
}

func (svc *JobExecutor) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	resp := &pb.ListResponse{}
	for _, jd := range svc.tracker.List(ctx, req.GetCompleted(), req.GetAllJobs()) {
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
	id, follow, ctx := string(req.GetJobId()), req.GetFollow(), stream.Context()
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

func (svc *JobExecutor) Shutdown(ctx context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	count, err := svc.tracker.Shutdown(ctx)
	if err != nil {
		return nil, err
	}

	close(svc.done)

	return &pb.ShutdownResponse{NumJobsStopped: int32(count)}, nil
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
