package service

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	pb "github.com/camh-/jobber/pb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeJob struct {
	status *pb.JobStatus
	logs   []string
}

var fakeJobs = map[string]fakeJob{
	"greeting-01234567": {
		status: &pb.JobStatus{
			JobId:     []byte("greeting-01234567"),
			State:     pb.JobStatus_JOBSTATE_RUNNING,
			StartTime: &timestamppb.Timestamp{Seconds: 1653654244},
			User:      "eve",
		},
		logs: []string{"Hello world\n", "Goodbye world\n"},
	},
	"jack-01234568": {
		status: &pb.JobStatus{
			JobId:     []byte("jack-01234568"),
			State:     pb.JobStatus_JOBSTATE_COMPLETED,
			ExitCode:  1,
			StartTime: &timestamppb.Timestamp{Seconds: 1653654245},
			User:      "mallory",
		},
		logs: []string{"fee\n", "fi\n", "fo\n", "fum\n"},
	},
	"red-01234569": {
		status: &pb.JobStatus{
			JobId:     []byte("red-01234569"),
			State:     pb.JobStatus_JOBSTATE_RUNNING,
			StartTime: &timestamppb.Timestamp{Seconds: 1653654246},
			User:      "mallory",
		},
		logs: []string{"too hot\n", "too cold\n", "just right\n"},
	},
}

type FakeJobExecutor struct {
	pb.UnimplementedJobExecutorServer
}

func NewFake() *FakeJobExecutor {
	return &FakeJobExecutor{}
}

func (svc *FakeJobExecutor) RegisterWith(gs grpc.ServiceRegistrar) {
	pb.RegisterJobExecutorServer(gs, svc)
}

func (svc *FakeJobExecutor) Run(ctx context.Context, req *pb.RunRequest) (*pb.RunResponse, error) {
	argv := append([]string{req.Spec.GetCommand()}, req.Spec.GetArguments()...)
	switch strings.Join(argv, " ") {
	case "greeting":
		return &pb.RunResponse{JobId: []byte("greeting-01234567")}, nil
	case "jack beanstalk":
		return &pb.RunResponse{JobId: []byte("jack-01234568")}, nil
	case "red riding hood":
		return &pb.RunResponse{JobId: []byte("red-01234569")}, nil
	default:
		return nil, fmt.Errorf("no such file or directory: %s", req.Spec.GetCommand())
	}
}

func (svc *FakeJobExecutor) Stop(ctx context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	_, ok := fakeJobs[string(req.GetJobId())]
	if !ok {
		return nil, fmt.Errorf("no such job: %s", req.GetJobId())
	}
	return &pb.StopResponse{}, nil
}

func (svc *FakeJobExecutor) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	j, ok := fakeJobs[string(req.GetJobId())]
	if !ok {
		return nil, fmt.Errorf("no such job: %s", req.GetJobId())
	}
	return &pb.StatusResponse{Status: j.status}, nil
}

func (svc *FakeJobExecutor) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	const user = "eve" // simulates authed user making request
	resp := &pb.ListResponse{}
	for _, j := range fakeJobs {
		if j.status.GetUser() != user && !req.AllJobs {
			continue
		}
		if j.status.GetState() == pb.JobStatus_JOBSTATE_COMPLETED && !req.Completed {
			continue
		}
		resp.Jobs = append(resp.Jobs, j.status)
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

func (svc *FakeJobExecutor) Logs(req *pb.LogsRequest, stream pb.JobExecutor_LogsServer) error {
	j, ok := fakeJobs[string(req.GetJobId())]
	if !ok {
		return fmt.Errorf("no such job: %s", req.GetJobId())
	}

	for _, line := range j.logs {
		resp := pb.LogsResponse{
			Line:      []byte(line),
			Timestamp: timestamppb.Now(),
		}
		if err := stream.Send(&resp); err != nil {
			return err
		}
	}
	return nil
}
