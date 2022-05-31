#!/bin/bash
#
# Integration test for CI
#

CERTS=cli/testdata

# output of `make build`
JOBBER=./out/jobber
tmjobber() {
    timeout -v 5 \
        "${JOBBER}" \
        "$1" \
        --tls-cert "${CERTS}/user.crt" \
        --tls-key "${CERTS}/user.key" \
        --ca-cert "${CERTS}/ca.crt" \
        "${@:2}"
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
main() {
  set -euo pipefail

  sudo --background ${JOBBER} serve \
      --tls-cert "${CERTS}/server.crt" \
      --tls-key "${CERTS}/server.key" \
      --ca-cert "${CERTS}/ca.crt" \
      --admin 'user' 
  sleep 1  # give the server a chance to start

  trap 'runjobber shutdown' EXIT

  test_stop
  test_logs
  test_status
  test_list
  test_bad_client_cert
  test_bad_server_cert
}

#-----------------------------------------------------------------------------

test_stop() {
  jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
  tmjobber stop "${jobid}"
  list_jobid=$(tmjobber list | tail -n +2 | awk '{print $1}')
  assert_equal "" "${list_jobid}"
}

test_logs() {
  want="hello world"
  jobid=$(tmjobber run -d /bin/echo "${want}" | awk '{print $3}')
  got=$(tmjobber logs -f -T "${jobid}")
  assert_equal "${want}" "${got}"
}

test_status() {
  jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
  list_jobid=$(tmjobber status "${jobid}" | tail -n +2 | awk '{print $1}')
  assert_equal "${jobid}" "${list_jobid}"
  tmjobber stop "${jobid}"
}

test_list() {
  jobid=$(tmjobber run -d /bin/sleep 100 | awk '{print $3}')
  list_jobid=$(tmjobber list | tail -n +2 | awk '{print $1}')
  assert_equal "${jobid}" "${list_jobid}"
  tmjobber stop "${jobid}"
}

test_bad_client_cert() {
    assert_failure tmjobber list \
        --tls-cert "${CERTS}/baduser.crt" \
        --tls-key "${CERTS}/baduser.key"
}

test_bad_server_cert() {
    # Use a different CA for the client so we do not trust the server
    assert_failure tmjobber list \
        --ca-cert "${CERTS}/badca.crt"
}

#-----------------------------------------------------------------------------

assert_equal() {
    want="$1"
    got="$2"

    if [[ "${want}" != "${got}" ]]; then
        printf 'FAIL: %s: want "%s", got "%s"\n' "${FUNCNAME[1]}" "${want}" "${got}"
        return 1
    fi
    return 0
}

assert_failure() {
    if output=$("$@" 2>&1); then
        printf 'FAIL: command exited successfully: %s\n' "$*"
        printf 'OUTPUT: %s\n' "${output}"
        return 1
    fi
    return 0
}

#-----------------------------------------------------------------------------
[[ "$(caller)" != 0\ * ]] || main "$@"

