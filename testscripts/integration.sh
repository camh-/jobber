#!/bin/bash
#
# Integration test for CI.
#
# This should be run through `make integration-test` as that first creates
# the certs this script requires, if necessary.
#

# directory of certs
CERTS=cli/testdata

# output of `make build`
JOBBER=./out/jobber

# Use a different port for testing as the developer may have a server running
# already on the default port (8443).
export JOBBER_SERVER=localhost:8444

#-----------------------------------------------------------------------------
main() {
	set -euo pipefail

	start_jobber_server

	test_stop
	test_logs
	test_status
	test_list
	test_list_all

	test_authz_stop
	test_authz_logs
	test_authz_status
	test_authz_list
	test_authz_list_all

	test_authn_bad_client_cert
	test_authn_bad_server_cert
	test_authn_tls_version
}

#-----------------------------------------------------------------------------

start_jobber_server() {
	sudo --background ${JOBBER} serve \
		--listen="${JOBBER_SERVER}" \
		--tls-cert "${CERTS}/server.crt" \
		--tls-key "${CERTS}/server.key" \
		--ca-cert "${CERTS}/ca.crt" \
		--admin 'user'
	sleep 1 # give the server a chance to start

	trap 'runjobber shutdown' EXIT
}

#-----------------------------------------------------------------------------

test_stop() {
	jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	tmjobber stop "${jobid}"
	list_jobid=$(tmjobber list | tail -n +2 | awk '{print $1}')
	assert_equal "" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_logs() {
	want="hello world"
	jobid=$(tmjobber run -d /bin/echo "${want}" | awk '{print $3}')
	got=$(tmjobber logs -f -T "${jobid}")
	assert_equal "${want}" "${got}"
	tmjobber stop -c "${jobid}"
}

test_status() {
	jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	list_jobid=$(tmjobber status "${jobid}" | tail -n +2 | awk '{print $1}')
	assert_equal "${jobid}" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_list() {
	jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	list_jobid=$(tmjobber list | tail -n +2 | awk '{print $1}')
	assert_equal "${jobid}" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_list_all() {
	jobid1=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	jobid2=$(tmujobber user2 run -d /bin/echo hello world | awk '{print $3}')
	jobid3=$(tmjobber run -d /bin/sleep 101 | awk '{print $3}')
	jobid4=$(tmjobber run -d /bin/echo goodbye world | awk '{print $3}')
	# shellcheck disable=SC2046,SC2005 # I want to word split
	list_jobid=$(echo $(tmjobber list -a -c | tail -n +2 | awk '{print $1}'))
	assert_equal "${jobid1} ${jobid2} ${jobid3} ${jobid4}" "${list_jobid}"
	tmjobber stop -c "${jobid1}"
	tmjobber stop -c "${jobid2}"
	tmjobber stop -c "${jobid3}"
	tmjobber stop -c "${jobid4}"
}

test_authz_stop() {
	# Start job as "user" (default)
	jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	# Try to stop as "user2"
	assert_failure tmujobber user2 stop "${jobid}"
	list_jobid=$(tmjobber list | tail -n +2 | awk '{print $1}')
	assert_equal "${jobid}" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_authz_logs() {
	# Start job as "user" (default)
	want="hello world"
	jobid=$(tmjobber run -d /bin/echo "${want}" | awk '{print $3}')
	# Try to get logs as "user2"
	assert_failure tmujobber user2 logs "${jobid}"
	tmjobber stop -c "${jobid}"
}

test_authz_status() {
	# Start job as "user" (default)
	jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	# Try to get status as "user2"
	assert_failure tmujobber user2 status "${jobid}"
	list_jobid=$(tmjobber list | tail -n +2 | awk '{print $1}')
	assert_equal "${jobid}" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_authz_list() {
	# Start job as "user" (default)
	jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
	# List all jobs as "user2" - should not see "user"s jobs
	list_jobid=$(tmujobber user2 list --all | tail -n +2 | awk '{print $1}')
	assert_equal "" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_authz_list_all() {
	# Start job as "user2"
	jobid=$(tmujobber user2 run -d /bin/sleep 100 | awk '{print $3}')
	# List all jobs as "user" (admin) - should see "user2"s jobs
	list_jobid=$(tmjobber list --all | tail -n +2 | awk '{print $1}')
	assert_equal "${jobid}" "${list_jobid}"
	tmjobber stop -c "${jobid}"
}

test_authn_bad_client_cert() {
	# Use key/cert signed by a different CA to what the server expects
	assert_failure tmjobber list \
		--tls-cert "${CERTS}/baduser.crt" \
		--tls-key "${CERTS}/baduser.key"
}

test_authn_bad_server_cert() {
	# Use a different CA for the client so we do not trust the server
	assert_failure tmjobber list \
		--ca-cert "${CERTS}/badca.crt"
}

test_authn_tls_version() {
	# We can't use `jobber` to test this because it always uses TLS 1.3
	# Just use curl to max at tls 1.2
	assert_error "alert protocol version" \
		curl --tls-max 1.2 "https://${JOBBER_SERVER}/"
}

#-----------------------------------------------------------------------------

assert_equal() {
	want="$1"
	got="$2"

	if [[ "${want}" != "${got}" ]]; then
		fail 'want "%s", got "%s"' "${want}" "${got}"
		return 1
	fi
	return 0
}

assert_failure() {
	if output=$("$@" 2>&1); then
		printf 'OUTPUT: %s\n' "${output}"
		fail 'expected failed, got success: %s' "$*"
		return 1
	fi
	return 0
}

assert_error() {
	error="$1"
	shift
	if output=$("$@" 2>&1); then
		printf 'OUTPUT: %s\n' "${output}"
		fail 'expected failed, got success: %s' "$*"
		return 1
	fi

	if ! [[ "${output}" =~ ${error} ]]; then
		printf 'OUTPUT: %s\n' "${output}"
		fail 'did not get error: %s' "${error}"
		return 1
	fi

	return 0
}

fail() {
	format="$1"
	shift
	printf "FAIL: %s: ${format}\n" "${FUNCNAME[2]}" "$@"
	return 1
}

#-----------------------------------------------------------------------------

tmjobber() {
	timeout -v 5 \
		"${JOBBER}" \
		"$1" \
		--tls-cert "${CERTS}/user.crt" \
		--tls-key "${CERTS}/user.key" \
		--ca-cert "${CERTS}/ca.crt" \
		"${@:2}"
}

tmujobber() {
	timeout -v 5 \
		"${JOBBER}" \
		"$2" \
		--tls-cert "${CERTS}/$1.crt" \
		--tls-key "${CERTS}/$1.key" \
		--ca-cert "${CERTS}/ca.crt" \
		"${@:3}"
}

runjobber() {
	"${JOBBER}" \
		"$1" \
		--tls-cert "${CERTS}/user.crt" \
		--tls-key "${CERTS}/user.key" \
		--ca-cert "${CERTS}/ca.crt" \
		"${@:2}"
}

#-----------------------------------------------------------------------------
[[ "$(caller)" != 0\ * ]] || main "$@"
