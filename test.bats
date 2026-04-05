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
  rm -f /tmp/bats-shebang-*
  rm -f /tmp/bats-shell-directive-*
  rm -f /tmp/bats-pre-stop-hooks-*
  rm -f /tmp/bats-post-start-hooks-*
}

teardown() {
  for project in bats-pre-stop bats-post-stop bats-both bats-sync bats-shebang-sh bats-shebang-bash bats-shebang-dash bats-shebang-python3 bats-shebang-default bats-exited-cleanup bats-pre-stop-hooks bats-post-start-hooks bats-multi-file; do
    docker compose -p "$project" down --remove-orphans --timeout 5 2>/dev/null || true
  done

  docker compose -p bats-shell-directive down --remove-orphans --volumes --timeout 5 2>/dev/null || true
  docker compose -p bats-pre-stop-hooks down --remove-orphans --volumes --timeout 5 2>/dev/null || true
  docker compose -p bats-post-start-hooks down --remove-orphans --volumes --timeout 5 2>/dev/null || true

  rm -f /tmp/bats-detached-*
  rm -f /tmp/bats-sync-*
  rm -f /tmp/bats-shebang-*
  rm -f /tmp/bats-shell-directive-*
  rm -f /tmp/bats-pre-stop-hooks-*
  rm -f /tmp/bats-post-start-hooks-*
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

@test "shebang sh executes correctly" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/shebang-sh"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-sh web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-sh web
  echo "output: $output"
  echo "status: $status"
  assert_success

  [ -f /tmp/bats-shebang-sh-completed ]
}

@test "shebang bash executes correctly" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/shebang-bash"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-bash web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-bash web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # marker is only created if BASH_VERSION is set, proving bash ran
  [ -f /tmp/bats-shebang-bash-completed ]
}

@test "shebang dash executes correctly" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/shebang-dash"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-dash web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-dash web
  echo "output: $output"
  echo "status: $status"
  assert_success

  [ -f /tmp/bats-shebang-dash-completed ]
}

@test "shebang python3 executes correctly" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/shebang-python3"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-python3 web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-python3 web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # marker is created via python3 pathlib, proving python3 ran
  [ -f /tmp/bats-shebang-python3-completed ]
}

@test "default shebang uses sh" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/shebang-default"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-default web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shebang-default web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # no shebang in script, should default to #!/bin/sh
  [ -f /tmp/bats-shebang-default-completed ]
}

@test "SHELL directive /bin/bash -c is detected for container scripts" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/shell-directive"

  # build the custom image with SHELL ["/bin/bash", "-c"]
  docker compose -p bats-shell-directive build

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shell-directive web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-shell-directive web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # verify host command ran
  [ -f /tmp/bats-shell-directive-host-completed ]

  # check that the container script ran at all
  run docker run --rm -v bats-shell-directive_shell-test:/data nginx:latest cat /data/executed
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "script-ran" "$output"

  # check that bash was used (image SHELL directive detected)
  # the script uses [[ ]] which only works in bash, not dash/sh
  run docker run --rm -v bats-shell-directive_shell-test:/data nginx:latest cat /data/result
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "bash-ok" "$output"
}

@test "exited containers are cleaned up before deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/exited-container-cleanup"

  # Initial deploy: 3 running containers via docker-orchestrate
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-exited-cleanup web
  echo "output: $output"
  echo "status: $status"
  assert_success
  run docker ps --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=running" -q
  echo "initial running containers: $output"
  assert_equal "3" "$(echo "$output" | wc -l | tr -d ' ')"

  # Get container IDs for manipulation
  container_to_stop=$(docker ps --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=running" -q | sed -n '1p')
  container_to_kill=$(docker ps --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=running" -q | sed -n '2p')

  # Stop one container gracefully (exit code 0)
  docker stop "$container_to_stop"

  # Kill another container with SIGKILL (non-zero exit code)
  docker kill --signal=KILL "$container_to_kill"

  # Verify intermediate state: 1 running, 2 exited
  run docker ps --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=running" -q
  echo "running after stop/kill: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"

  run docker ps -a --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=exited" -q
  echo "exited after stop/kill: $output"
  assert_equal "2" "$(echo "$output" | wc -l | tr -d ' ')"

  # Verify exit codes: one should be 0, one should be non-zero
  exited_container_0=$(docker ps -a --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=exited" -q | sed -n '1p')
  exited_container_1=$(docker ps -a --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=exited" -q | sed -n '2p')
  exit_code_0=$(docker inspect --format '{{.State.ExitCode}}' "$exited_container_0")
  exit_code_1=$(docker inspect --format '{{.State.ExitCode}}' "$exited_container_1")
  echo "exit codes: $exit_code_0, $exit_code_1"

  # One should be 0 and one should be non-zero (137 for SIGKILL)
  if [[ "$exit_code_0" -eq 0 && "$exit_code_1" -ne 0 ]] || [[ "$exit_code_0" -ne 0 && "$exit_code_1" -eq 0 ]]; then
    echo "exit code verification passed: one is 0, one is non-zero"
  else
    flunk "expected one exit code 0 and one non-zero, got: $exit_code_0 and $exit_code_1"
  fi

  # Redeploy with --replicas 3: should clean up exited containers and reach 3 running
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-exited-cleanup --replicas 3 web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify final state: 3 running, 0 exited
  run docker ps --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=running" -q
  echo "final running: $output"
  assert_equal "3" "$(echo "$output" | wc -l | tr -d ' ')"

  run docker ps -a --filter "label=com.docker.compose.project=bats-exited-cleanup" --filter "status=exited" -q
  echo "final exited: $output"
  assert_equal "" "$output"
}

@test "compose spec pre_stop hooks execute inside container" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pre-stop-hooks"

  export BATS_HOST_TEST_VAR="interpolated-ok"
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pre-stop-hooks web
  echo "output: $output"
  echo "status: $status"
  assert_success

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pre-stop-hooks web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify host commands ran
  [ -f /tmp/bats-pre-stop-hooks-host-completed ]
  [ -f /tmp/bats-pre-stop-hooks-post-completed ]

  # Verify x-pre-stop-command ran inside container
  run docker run --rm -v bats-pre-stop-hooks_hook-test:/data nginx:latest cat /data/pre-stop-cmd
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "script-ran" "$output"

  # Verify first pre_stop hook ran
  run docker run --rm -v bats-pre-stop-hooks_hook-test:/data nginx:latest cat /data/hook1
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "hook1-ran" "$output"

  # Verify second pre_stop hook ran
  run docker run --rm -v bats-pre-stop-hooks_hook-test:/data nginx:latest cat /data/hook2
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "hook2-ran" "$output"

  # Verify third hook with direct environment value
  run docker run --rm -v bats-pre-stop-hooks_hook-test:/data nginx:latest cat /data/hook3
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "env-ok" "$output"

  # Verify fourth hook with compose-level ${VAR} interpolation from host env
  run docker run --rm -v bats-pre-stop-hooks_hook-test:/data nginx:latest cat /data/hook4
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "interpolated-ok" "$output"
}

@test "compose spec post_start hooks execute inside container during scale-up" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/post-start-hooks"

  export BATS_HOST_TEST_VAR="interpolated-ok"
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-post-start-hooks web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify first post_start hook ran
  run docker run --rm -v bats-post-start-hooks_hook-test:/data nginx:latest cat /data/hook1
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "hook1-ran" "$output"

  # Verify second post_start hook ran
  run docker run --rm -v bats-post-start-hooks_hook-test:/data nginx:latest cat /data/hook2
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "hook2-ran" "$output"

  # Verify third hook with direct environment value
  run docker run --rm -v bats-post-start-hooks_hook-test:/data nginx:latest cat /data/hook3
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "env-ok" "$output"

  # Verify fourth hook with compose-level ${VAR} interpolation from host env
  run docker run --rm -v bats-post-start-hooks_hook-test:/data nginx:latest cat /data/hook4
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "interpolated-ok" "$output"
}

@test "multiple compose files via repeated --file flag" {
  run "$DOCKER_ORCHESTRATE" deploy \
    --file "${BATS_TEST_DIRNAME}/tests/fixtures/multiple-files/docker-compose.yaml" \
    --file "${BATS_TEST_DIRNAME}/tests/fixtures/multiple-files/docker-compose.override.yaml" \
    --project-name bats-multi-file web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify the override was applied by checking the env var on the running container
  container_id=$(docker ps --filter "label=com.docker.compose.project=bats-multi-file" --filter "status=running" -q | head -1)
  run docker exec "$container_id" printenv OVERRIDE_APPLIED
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "true" "$output"
}
