#!/usr/bin/env bats

export SYSTEM_NAME="$(uname -s | tr '[:upper:]' '[:lower:]')"

ARCH="$(uname -m)"
case "$ARCH" in
x86_64) ARCH="amd64" ;;
aarch64) ARCH="arm64" ;;
arm64) ARCH="arm64" ;;
esac
export DOCKER_ORCHESTRATE="${BATS_TEST_DIRNAME}/build/${SYSTEM_NAME}/docker-orchestrate-${ARCH}"

flunk() {
  {
    if [[ "$#" -eq 0 ]]; then
      cat -
    else
      echo "$*"
    fi
  }
  return 1
}

assert_equal() {
  if [[ "$1" != "$2" ]]; then
    {
      echo "expected: $1"
      echo "actual:   $2"
    } | flunk
  fi
}

assert_exit_status() {
  exit_status="$1"
  if [[ "$status" -ne "$exit_status" ]]; then
    {
      echo "expected exit status: $exit_status"
      echo "actual exit status:   $status"
    } | flunk
    flunk
  fi
}

assert_failure() {
  if [[ "$status" -eq 0 ]]; then
    flunk "expected failed exit status"
  elif [[ "$#" -gt 0 ]]; then
    assert_output "$1"
  fi
}

assert_success() {
  if [[ "$status" -ne 0 ]]; then
    flunk "command failed with exit status $status"
  elif [[ "$#" -gt 0 ]]; then
    assert_output "$1"
  fi
}

assert_output() {
  local expected
  if [[ $# -eq 0 ]]; then
    expected="$(cat -)"
  else
    expected="$1"
  fi
  assert_equal "$expected" "$output"
}

assert_output_contains() {
  local input="$output"
  local expected="$1"
  local count="${2:-1}"
  local found=0
  until [ "${input/$expected/}" = "$input" ]; do
    input="${input/$expected/}"
    found=$((found + 1))
  done
  assert_equal "$count" "$found"
}

assert_output_not_exists() {
  [[ -z "$output" ]] || flunk "expected no output, found some"
}

setup_file() {
  docker pull nginx:latest
}

setup() {
  rm -f /tmp/bats-detached-*
  rm -f /tmp/bats-sync-*
}

teardown() {
  for project in bats-pre-stop bats-post-stop bats-both bats-sync; do
    docker compose -p "$project" down --remove-orphans --timeout 5 2>/dev/null || true
  done

  rm -f /tmp/bats-detached-*
  rm -f /tmp/bats-sync-*
}

@test "default" {
  run true
  echo "output: $output"
  echo "status: $status"
  assert_success
}

@test "pre-stop host command detached continues running after exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/detached-pre-stop"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pre-stop web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pre-stop web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # started marker should exist (command was launched)
  [ -f /tmp/bats-detached-pre-stop-started ]

  # completed marker should NOT exist yet (still running in background)
  [ ! -f /tmp/bats-detached-pre-stop-completed ]

  # wait for the detached command to finish
  sleep 7

  # completed marker should now exist (process ran to completion independently)
  [ -f /tmp/bats-detached-pre-stop-completed ]
}

@test "post-stop host command detached continues running after exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/detached-post-stop"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-post-stop web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-post-stop web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # started marker should exist (command was launched)
  [ -f /tmp/bats-detached-post-stop-started ]

  # completed marker should NOT exist yet (still running in background)
  [ ! -f /tmp/bats-detached-post-stop-completed ]

  # wait for the detached command to finish
  sleep 7

  # completed marker should now exist (process ran to completion independently)
  [ -f /tmp/bats-detached-post-stop-completed ]
}

@test "both pre-stop and post-stop detached commands continue running after exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/detached-both"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-both web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-both web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # both started markers should exist
  [ -f /tmp/bats-detached-both-pre-started ]
  [ -f /tmp/bats-detached-both-post-started ]

  # neither completed marker should exist yet
  [ ! -f /tmp/bats-detached-both-pre-completed ]
  [ ! -f /tmp/bats-detached-both-post-completed ]

  # wait for the detached commands to finish
  sleep 7

  # both completed markers should now exist
  [ -f /tmp/bats-detached-both-pre-completed ]
  [ -f /tmp/bats-detached-both-post-completed ]
}

@test "non-detached commands complete before exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/non-detached-baseline"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-sync web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-sync web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # both started AND completed markers should exist immediately
  # because synchronous commands block until done
  [ -f /tmp/bats-sync-pre-started ]
  [ -f /tmp/bats-sync-pre-completed ]
  [ -f /tmp/bats-sync-post-started ]
  [ -f /tmp/bats-sync-post-completed ]
}
