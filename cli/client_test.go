package cli

import (
	"bytes"
	"io"
	"net"
	"testing"

	"github.com/camh-/jobber/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestClientAgainstFakeService(t *testing.T) {
	grpcServer := grpc.NewServer()
	jobberService := service.NewFake()
	jobberService.RegisterWith(grpcServer)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := lis.Addr().String()
	go grpcServer.Serve(lis) //nolint:errcheck
	defer grpcServer.Stop()

	t.Run("run greeting", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdRun{
			clientCmd:    clientCmd{Address: address, output: w},
			NoTimestamps: true,
			Command:      "greeting",
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `job id: greeting-01234567
Hello world
Goodbye world
`
		require.Equal(t, expected, w.String())
	})

	t.Run("run jack beanstalk", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdRun{
			clientCmd:    clientCmd{Address: address, output: w},
			NoTimestamps: true,
			Command:      "jack",
			Args:         []string{"beanstalk"},
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `job id: jack-01234568
fee
fi
fo
fum
`
		require.Equal(t, expected, w.String())
	})

	t.Run("run invalid-command", func(t *testing.T) {
		cmd := CmdRun{
			clientCmd:    clientCmd{Address: address, output: io.Discard},
			NoTimestamps: true,
			Command:      "invalid-command",
		}
		err := cmd.Run()
		require.Error(t, err)
	})

	t.Run("stop greeting-01234567", func(t *testing.T) {
		cmd := CmdStop{
			clientCmd: clientCmd{Address: address, output: io.Discard},
			JobID:     "greeting-01234567",
		}
		err := cmd.Run()
		require.NoError(t, err)
	})

	t.Run("stop invalid-job-id", func(t *testing.T) {
		cmd := CmdStop{
			clientCmd: clientCmd{Address: address, output: io.Discard},
			JobID:     "invalid-job-id",
		}
		err := cmd.Run()
		require.Error(t, err)
	})

	t.Run("status greeting-01234567", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdStatus{
			clientCmd: clientCmd{Address: address, output: w},
			JobID:     "greeting-01234567",
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `JOB ID             START TIME       USER  STATUS
greeting-01234567  May 27 12:24:04  eve   running
`
		require.Equal(t, expected, w.String())
	})

	t.Run("status invalid-job-id", func(t *testing.T) {
		cmd := CmdStatus{
			clientCmd: clientCmd{Address: address, output: io.Discard},
			JobID:     "invalid-job-id",
		}
		err := cmd.Run()
		require.Error(t, err)
	})

	t.Run("list", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdList{
			clientCmd: clientCmd{Address: address, output: w},
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `JOB ID             START TIME       USER  STATUS
greeting-01234567  May 27 12:24:04  eve   running
`
		require.Equal(t, expected, w.String())
	})

	t.Run("list all running", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdList{
			clientCmd: clientCmd{Address: address, output: w},
			All:       true,
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `JOB ID             START TIME       USER     STATUS
greeting-01234567  May 27 12:24:04  eve      running
red-01234569       May 27 12:24:06  mallory  running
`
		require.Equal(t, expected, w.String())
	})

	t.Run("list all", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdList{
			clientCmd: clientCmd{Address: address, output: w},
			All:       true,
			Completed: true,
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `JOB ID             START TIME       USER     STATUS
greeting-01234567  May 27 12:24:04  eve      running
jack-01234568      May 27 12:24:05  mallory  exited (1)
red-01234569       May 27 12:24:06  mallory  running
`
		require.Equal(t, expected, w.String())
	})

	t.Run("logs greeting-01234567", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdLogs{
			clientCmd:    clientCmd{Address: address, output: w},
			JobID:        "greeting-01234567",
			NoTimestamps: true,
		}
		err := cmd.Run()
		require.NoError(t, err)
		expected := `Hello world
Goodbye world
`
		require.Equal(t, expected, w.String())
	})

	t.Run("logs invalid-job-id", func(t *testing.T) {
		cmd := CmdLogs{
			clientCmd: clientCmd{Address: address, output: io.Discard},
			JobID:     "invalid-job-id",
		}
		err := cmd.Run()
		require.Error(t, err)
	})
}
