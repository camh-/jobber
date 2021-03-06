---
authors: Cam Hutchison (camh@xdna.net)
state: draft
---

# Remote Job Runner Service

## What

A remotely-accessible service for running jobs on a host and streaming output
from the jobs to a client. Resources and namespaces for jobs can be constrained
via the Linux cgroups API and the clone(2) namespace flags. Clients are
authenticated with mutual-TLS, where the client certificate asserts the user's
identity used for authorization.

## Why

* To demonstrate the ability to communicate clearly in design documents and in
  code.
* To get a job at Teleport, implementing the [systems challenge at level 5].

[systems challenge at level 5]: https://github.com/gravitational/careers/blob/main/challenges/systems/challenge.md#level-5

## Details

There are three main components the design:

1. **Library**: Go packages to run individual jobs and track a collection of jobs,
2. **Service/API**: A gRPC service allowing authenticated users to run jobs,
3. **CLI**: A command-line program to perform actions on a service instance.

### Library

The library consists of two primary packages: `job` and `jobtracker`.

The purpose of the `job` package is to represent a single running job, allowing
it to be created, watched, stopped and reaped. It exists as a simple reusable
component for programs that want to run individual jobs within a container (a
container being a cgroup and namespaces).

The `jobtracker` maintains a set of `job`s, allowing known jobs to be listed and
individual jobs to be looked up by an ID so that the job can be operated upon.

Executing a job in a container will be a two-step process as it is problematic
to fork in Go, and the typical approach to using cgroups and namespaces is to
fork, set up the container and then execute the binary to run in that container.
Instead, the library will allow a pluggable executor that can run a program as a
child process that sets up the container and then execs the program.

The executor will be given a job specification containing the cgroup and
namespace parameters, command and arguments and any other setup required for the
job. It should spawn a child process in new namespaces where appropriate
(using the `CLONE_*` flags) and pass the job specification via the command line.
It then re-creates the job specification from the command line and calls back
into the `job` library to complete the execution of the job.

Typically the executor will just run `/proc/self/exe` and the user of the
library will implement command line flags for re-creating the job specification
to pass back to the library, but it could run an entirely different program that
executes the job specification if so desired.

A `Job` will support getting the combined stdout and stderr output stream of it
captured from the start of the job. Multiple output streams requested of the
same job are independent and stream the same output. The stream comprises lines
with a maximum length of 512 bytes (size subject to revision). If a line is
longer than that, either because it is a long line of text or if the output is
binary data, it will be split into chunks. Each line/chunk has a timestamp of
when it was read from the job. A library client may reconstruct the output by
ignoring timestamps and concatenating elements of the output stream.

A successfully started job will be assigned a job ID comprising the basename of
the job's command (basename being the part after the last slash) and a random 8
hex digit suffix. It will be unique amongst all tracked jobs of a `jobtracker`.
Jobs are tracked after they have exited so as to retain the exit code and the
output, but can be cleaned up and removed from being tracked as requested. The
job ID can be used with the `jobtracker` to look up job status and output.

In the library, a job ID will be a Go `string`, but it may not be utf-8 encoded
as there is no such requirement on filenames in the filesystem and as the name
of the command is used in the ID, it cannot be guaranteed to be utf-8. In the
protobuf spec, the type will be `bytes` as a protobuf `string` must be utf-8
encoded.

The running jobs, exited job statuses and job output are not stored
persistently. If the process running the jobs restarts, it will restart with
empty state.

When a job is stopped, it will be sent a SIGTERM signal. If it has not exited
within a grace period of 10 seconds (an arbitrary timeout initially, extendable
via a stop parameter in future), it will be sent SIGKILL signal. A future
enhancement would be to kill all processes in the PID namespace, rather than
leaving it up to the top process of the hierarchy to propagate the signal.

#### Output streaming

The output of a job is its stdout and stderr streams. These will both be
connected to the same pipe that will be set up before the job executor is
called. This will collect the interleaved output of both these streams. Where
the streams are interleaved is up to the job itself and exactly when it writes
to each of stdout and stderr. A typical setup of the standard C library is to
buffer stdout but not stderr, so it may be that the output of the job is not
received in the order that the application code of the job writes it. There is
not much to be done about this - we cannot see the contents of an
application-level buffer.

The server library will contain a "log distributor" that is responsible for
reading the pipe from the job, storing it in a buffer and feeding it to any
client that is streaming the output of that job. Each job has a single log
distributor.

A "reader" goroutine will read from the pipe attached to stdout/stderr of the
job splitting the input into lines with a maximum size of 512 bytes. It will
attach a timestamp to that line marking the time the line was read, as described
above. For each line, it will send that on a channel to a "distributor" goroutine.

A "distributor" goroutine is responsible for reading the logs from the reader
goroutine and storing them in an in-memory buffer. It also accepts any number of
"writer" channels to stream out the logs. The distributor owns the logs buffer
and is the only goroutine that reads or writes to the buffer.

Each `LogsRequest` from a client attaches to the distributor and provides a
channel for the distributor to send logs to. The distributor maintains a set of
all connected clients and a cursor position of which line each is up to. As a
client channel becomes ready, the next line is sent on the channel to that
client. The client, running in its own goroutine, will receive from the channel
and issue a gRPC `SendMesg()` to stream that log back to the client.

If a client is following the logs (that is, we do not disconnect it after the
last line is sent, instead continuously streaming logs as they are generated),
and that client reaches the end, it will be temporarily disabled until more logs
are received from the job, at which point it will be re-enabled.

A slow client unable to receive at the same rate as other clients will not block
those other clients. Each client channel will only become ready for sending when
the client goroutine receives on that channel. In the mean time, other client
channels can become ready and receive logs. This also will not block the reader
goroutine.

#### Resource Limits

Certain resource limits can be specified when running a job and are controlled
using the Linux cgroup v2 functionality:

* CPU time in milliCPUs (i.e. 1000 mCPU gives the use of 1 CPU to the job)
* Memory in bytes
* I/O throughput per device in read/write bytes per second and IO operations per
  second,
* Maximum number of processes

These limits can be set to any valid value when running a job. There are no
limits on the number of jobs a user can run, nor a total of the above limits on
a per-user or per-group basis. Such aggregate limits are a possible future
enhancement.

#### Isolation

A job can be run under a filesystem root to prevent the job accessing any files
outside of that directory hierarchy. By default, a job runs in the same
filesystem namespace as the process running the job, apart from `/proc` which is
per-job. This will be implemented by putting each job in its own mount
namespace. Where a distinct filesystem root is not specified, `/proc` in the
container will overlay `/proc` of the host.

A job may be run isolated from the network with no network interfaces or may
share all the host's network interfaces - essentially the job runs with either
an empty network namespace or shares the network namespace of the job executor.

Every job runs in its own PID namespace - that is, it cannot see any process but
itself and its children, and the PIDs it can see are independent of others on
the system.

Subsequent iterations of this project may add the ability to specify which
network interfaces should be in the container. It is not planned to add any
network overlay capability through veth or tun/tap devices such as is available
with Kubernetes. The mount namespace may be extended by modelling a volume
concept to allow different storage and data to be mounted at different places in
the job's filesystem namespace. None of this will be done for the initial
implementation.

### Server/API

The server implements a [JobExecutor gRPC service](proto/jobexec.proto) to allow
jobs to be run, stopped, listed, queried and to stream back the output of jobs.

The gRPC server will bind to a cli-specified address and port, listening for
connections from clients. The connection will require mutual TLS - i.e. it will
require that the client present a certificate signed by a trusted authority and
it will use the Common Name (CN) in that certificate as the user's identity. See
the [Service Authentication](#service-authentication) below for more details.

Any program that the server is requested to run must already exist on the
machine where the server is running. The server will not download any files
(such as `docker run` does when pulling an image).

Errors from the execution of any gRPC methods will be returned to the gRPC
client using a gRPC error status response.

### CLI

A basic CLI will provide an interface to the server. The following command
structure will be supported. A user can only operate on jobs that they have
created, including listing and retrieving status and output, unless the user is
configured as an admin in the server configuration.

Run a job:

    jobber run [-d] command [args...]

If `-d` is provided, the cli detaches from the server. It will output the ID of
the job if successfully run, or an error otherwise. If `-d` is not specified,
after outputting the job ID, the output of the job will be streamed back from
the job. Killing the cli will not terminate the job. `jobber stop` must be used
for that.

To stop a running job:

    jobber stop [-c] job-id

The job specified by `job-id` is stopped if it is running, and if `-c` is
provided, it is removed from the list of completed jobs. Otherwise the job will
remain in the list of terminate processes until cleaned-up with `-c`.

To check the status of a running or completed job:

    jobber status job-id

To list jobs:

    jobber list [-c] [-a]

Only running jobs are listed, unless `-c` is provided in which case all jobs
(running and completed) are listed. Only jobs for the user are listed. If `-a`
is provided and the user is specified as an admin in the server config, then all
users' jobs are listed

To see the logs (output) of a job:

    jobber logs [-f] job-id

Output from the start of the job up to the current time is shown. If `-f` is
specified, the output will continue to be streamed in real-time as it is
generated.

### Security

#### Service Authentication

The server will require that the client present a certificate signed by a
trusted authority. The Common Name (CN) in the presented certificate will be
used as the user's identity for the purposes of authorization. A future
implementation would use the Organizational Units of the Subject to specify
groups that a user belongs to, to allow group/role-based authorization.

Trusted authorities are specified in a certificate bundle passed to the server
via the command line on launching the server. A bundle is a concatenation of
PEM-encoded X.509 certificates. Each certificate in the bundle is equally
trusted to authenticate users, although future enhancements could allow
particular authorities to have limited scopes for authorization (such as
allowing users trusted by a particular authority to have just read-only access
to job output, for instance).

If no certificate is presented by the client or the certificate presented by the
client is not signed by one of the trusted authorities, the client connection
will be closed. No gRPC requests will be accepted on the connection.

A Certificate Revocation List (CRL) will NOT be used in this implementation in
order to keep things simple. A worthwhile mitigation of leakage of client keys
is to make the signatures time-limited - i.e. client certificates can be issued
with a maximum of 12 hours or less, limiting the amount of time an exposed
client key can access the service.

Plaintext connection will not be accepted. Every connection to the service must
use TLS.

The CLI client will be able to be told to use a CA certificate bundle for
validating the server certificate. If not specified, the default system trust
store will be used (e.g. `/etc/ssl/certs` on Debian-based systems), as is the
default when no CA certificate trust store is provided.

If the client cannot validate the server certificate, it will terminate the
connection without sending any requests.

#### Authorization

A simple two-level authorization scheme will be used. Any authenticated user can
start a job, stop any jobs they started, list all jobs they started, and get the
status and output of any jobs they started. They cannot see or operate on any
job they did not start.

In addition, a set of admin users can be specified on the server command line. A
user with admin scope can operate on any job the server is running.

A full-featured implementation would have a broader list of scopes, giving
finer-grained control over each method, as well as restricting such things as
which programs can be executed, which mount and network namespaces can be used,
being able to constrain access and control to jobs executed by other users, etc.
Such an implementation would allow some form of grouping to be used to specify
authorization. Such groups could be presented in the client certificate
subject's Organizational Units field, or be specified by the authority issuing
the client certificate.

#### TLS

The service will follow the ["Modern compatibility" recommended by Mozilla][mozilla-modern]:

* Cipher suites (TLS 1.3): `TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256`
* Protocols: TLS 1.3
* Certificate type: ECDSA (P-256)
* TLS curves: X25519, prime256v1, secp384r1
* Cipher preference: client chooses

The certificate lifespan is not specified - it is up to the certificate issuer
to specify how long the client certificate is valid for. In a secure environment
with automated issuance of certificates, the lifespan should not be more than
one day. Organisational policies should dictate how long issued credentials are
valid.

[mozilla-modern]: https://wiki.mozilla.org/Security/Server_Side_TLS#Modern_compatibility

## References

* [Control Groups version 2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)
* [Mozilla Server Side TLS recommended configurations](https://wiki.mozilla.org/Security/Server_Side_TLS)
* [Containers from scratch](https://medium.com/@ssttehrani/containers-from-scratch-with-golang-5276576f9909)
* [clone3 in Go](https://github.com/golang/go/issues/51246)
* [RHEL cgroups-v2 CPU control](https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux/8/html/managing_monitoring_and_updating_the_kernel/using-cgroups-v2-to-control-distribution-of-cpu-time-for-applications_managing-monitoring-and-updating-the-kernel)
* [Containers From Scratch ??? Liz Rice ??? GOTO 2018](https://www.youtube.com/watch?v=8fi7uSYlOdc)
