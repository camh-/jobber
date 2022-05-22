---
authors: Cam Hutchison (camh@xdna.net)
state: draft
---

# Remote Job Runner Service

## What

A remotely-accessible service for running jobs on a host and streaming output
from the jobs to a client. Resources and namespaces for jobs can be constrained
via the Linux cgroups API. Clients are authenticated with mutual-TLS, where the
client certificate asserts the user's identity used for authorization.

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
component for programs that want to run individual jobs within a cgroup.

The `jobtracker` maintains a set of `job`s, allowing known jobs to be listed and
individual jobs to be looked up by an ID so that the job can be operated upon.

Executing a job in a cgroup will be a two-step process as it is problematic to
fork in Go, and the typical approach to using groups is to fork, set up the
cgroup and then execute the binary to run in that cgroup. Instead, the library
will allow a pluggable executor that can run a program as a child process that
sets up the cgroup and then execs the program. The executor can just run
`/proc/self/exe` with an argument list that can be parsed into a `JobSpec`
struct and passed back into the `job` package, which will then take care of
setting up the group and executing the program.

A `Job` will support getting the output stream of it captured from the start of
the job. Multiple output streams of the same job are independent and stream the
same output. Each line of output is timestamped with the time the line was read
from the job. The length of a line is capped where there is no newline before
the maximum length. This maximum length will initially be 512 bytes but may be
revised.

A successfully started job will be assigned a job ID comprising the basename of
the job's command (basename being the part after the last slash) and a random 8
hex digit suffix. It will be unique amongst all tracked jobs. Jobs are tracked
after they have exited so as to retain the exit code and the output, but can be
cleaned up and removed from being tracked as requested. The job ID can be used
with the `jobtracker` to look up job status and output.

In the library, a job ID will be a Go `string`, but it may not be utf-8 encoded
as there is no such requirement on filenames in the filesystem and as the name
of the command is used in the ID, it cannot be guaranteed to be utf-8. In the
protobuf spec, the type will be `bytes` as a protobuf `string` must be utf-8
encoded.

The running jobs, exited job statuses and job output are not stored
persistently. If the process running the jobs restarts, it will restart with
empty state.

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
filesystem namespace as the process running the job.

A job may be run isolated from the network with no network interfaces. Multiple
levels of future enhancement are possible here: run with just a loopback device,
set up network overlays and attach jobs to particular overlays, etc. None of
these will be in the initial implementation.

Every job runs in its own PID namespace - that is, it cannot see any process but
itself and its children, and the PIDs it can see are independent of others on
the system.

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
provided, it is removed from the list of terminated jobs. Otherwise the job will
remain in the list of terminate processes until cleaned-up with `-c`.

To check the status of a running or completed job:

    jobber status job-id

To list jobs:

    jobber list [-t] [-a]

Only running jobs are listed, unless `-t` is provided in which case all jobs
(running and terminated) are listed. Only jobs for the user are listed. If `-a`
is provided and the user is specified as an admin in the server config, then all
users' jobs are listed

To see the output of a job:

    jobber output [-f] job-id

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
* [Containers From Scratch • Liz Rice • GOTO 2018](https://www.youtube.com/watch?v=8fi7uSYlOdc)
