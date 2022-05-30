package cli

import (
	"bytes"
	"io"
	"net"
	"testing"

	"github.com/camh-/jobber/job"
	"github.com/camh-/jobber/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func newClientCmd(address string, output io.Writer) clientCmd {
	return clientCmd{
		Address: address,
		output:  output,
		TLSCert: "testdata/user.crt",
		TLSKey:  "testdata/user.key",
		CACert:  "testdata/ca.crt",
	}
}
func TestClientAgainstFakeService(t *testing.T) {
	creds, err := mTLSCreds("testdata/server.crt", "testdata/server.key", "testdata/ca.crt")
	require.NoError(t, err)

	grpcServer := grpc.NewServer(grpc.Creds(creds))
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
			clientCmd:    newClientCmd(address, w),
			NoTimestamps: true,
			JobSpec:      job.JobSpec{Command: "greeting"},
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
			clientCmd:    newClientCmd(address, w),
			NoTimestamps: true,
			JobSpec: job.JobSpec{
				Command: "jack",
				Args:    []string{"beanstalk"},
			},
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
			clientCmd:    newClientCmd(address, io.Discard),
			NoTimestamps: true,
			JobSpec:      job.JobSpec{Command: "invalid-command"},
		}
		err := cmd.Run()
		require.Error(t, err)
	})

	t.Run("stop greeting-01234567", func(t *testing.T) {
		cmd := CmdStop{
			clientCmd: newClientCmd(address, io.Discard),
			JobID:     "greeting-01234567",
		}
		err := cmd.Run()
		require.NoError(t, err)
	})

	t.Run("stop invalid-job-id", func(t *testing.T) {
		cmd := CmdStop{
			clientCmd: newClientCmd(address, io.Discard),
			JobID:     "invalid-job-id",
		}
		err := cmd.Run()
		require.Error(t, err)
	})

	t.Run("status greeting-01234567", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdStatus{
			clientCmd: newClientCmd(address, w),
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
			clientCmd: newClientCmd(address, io.Discard),
			JobID:     "invalid-job-id",
		}
		err := cmd.Run()
		require.Error(t, err)
	})

	t.Run("list", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdList{
			clientCmd: newClientCmd(address, w),
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
			clientCmd: newClientCmd(address, w),
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
			clientCmd: newClientCmd(address, w),
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
			clientCmd:    newClientCmd(address, w),
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

	t.Run("invalid client cert CA", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdRun{
			clientCmd: newClientCmd(address, w),
			Detach:    true,
			JobSpec:   job.JobSpec{Command: "greeting"},
		}
		cmd.TLSCert = "testdata/baduser.crt"
		cmd.TLSKey = "testdata/baduser.key"
		err := cmd.Run()
		// We don't get a good error message from the client connection,
		// just "broken pipe". Shame. XXX Look deeper.
		require.Error(t, err)
	})

}

func TestBadServerCerts(t *testing.T) {
	creds, err := mTLSCreds("testdata/badserver.crt", "testdata/badserver.key", "testdata/ca.crt")
	require.NoError(t, err)

	grpcServer := grpc.NewServer(grpc.Creds(creds))
	jobberService := service.NewFake()
	jobberService.RegisterWith(grpcServer)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := lis.Addr().String()
	go grpcServer.Serve(lis) //nolint:errcheck
	defer grpcServer.Stop()

	t.Run("invalid server cert CA", func(t *testing.T) {
		w := &bytes.Buffer{}
		cmd := CmdRun{
			clientCmd: newClientCmd(address, w),
			Detach:    true,
			JobSpec:   job.JobSpec{Command: "greeting"},
		}
		err := cmd.Run()
		require.ErrorContains(t, err, "x509: certificate signed by unknown authority")
	})
}
