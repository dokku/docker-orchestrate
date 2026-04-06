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
  rm -f /tmp/orch-*
}

teardown() {
  for project in $(docker compose ls -a -q 2>/dev/null | grep "^bats-"); do
    docker compose -p "$project" down --remove-orphans --volumes --timeout 5 2>/dev/null || true
  done

  rm -f /tmp/orch-*
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
  [ -f /tmp/orch-detached-pre-stop-started ]

  # completed marker should NOT exist yet (still running in background)
  [ ! -f /tmp/orch-detached-pre-stop-completed ]

  # poll for the detached command to finish (up to 25s)
  for i in $(seq 1 25); do
    [ -f /tmp/orch-detached-pre-stop-completed ] && break
    sleep 1
  done

  # completed marker should now exist (process ran to completion independently)
  [ -f /tmp/orch-detached-pre-stop-completed ]
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
  [ -f /tmp/orch-detached-post-stop-started ]

  # completed marker should NOT exist yet (still running in background)
  [ ! -f /tmp/orch-detached-post-stop-completed ]

  # poll for the detached command to finish (up to 25s)
  for i in $(seq 1 25); do
    [ -f /tmp/orch-detached-post-stop-completed ] && break
    sleep 1
  done

  # completed marker should now exist (process ran to completion independently)
  [ -f /tmp/orch-detached-post-stop-completed ]
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
  [ -f /tmp/orch-detached-both-pre-started ]
  [ -f /tmp/orch-detached-both-post-started ]

  # neither completed marker should exist yet
  [ ! -f /tmp/orch-detached-both-pre-completed ]
  [ ! -f /tmp/orch-detached-both-post-completed ]

  # poll for both detached commands to finish (up to 25s)
  for i in $(seq 1 25); do
    [ -f /tmp/orch-detached-both-pre-completed ] && [ -f /tmp/orch-detached-both-post-completed ] && break
    sleep 1
  done

  # both completed markers should now exist
  [ -f /tmp/orch-detached-both-pre-completed ]
  [ -f /tmp/orch-detached-both-post-completed ]
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
  [ -f /tmp/orch-sync-pre-started ]
  [ -f /tmp/orch-sync-pre-completed ]
  [ -f /tmp/orch-sync-post-started ]
  [ -f /tmp/orch-sync-post-completed ]
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

  [ -f /tmp/orch-shebang-sh-completed ]
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
  [ -f /tmp/orch-shebang-bash-completed ]
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

  [ -f /tmp/orch-shebang-dash-completed ]
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
  [ -f /tmp/orch-shebang-python3-completed ]
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
  [ -f /tmp/orch-shebang-default-completed ]
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
  [ -f /tmp/orch-shell-directive-host-completed ]

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
  [ -f /tmp/orch-pre-stop-hooks-host-completed ]
  [ -f /tmp/orch-pre-stop-hooks-post-completed ]

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

@test "env file via repeated --env-file flag" {
  run "$DOCKER_ORCHESTRATE" deploy \
    --file "${BATS_TEST_DIRNAME}/tests/fixtures/env-file/docker-compose.yaml" \
    --env-file "${BATS_TEST_DIRNAME}/tests/fixtures/env-file/custom.env" \
    --project-name bats-env-file web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify the env file was applied by checking the env var on the running container
  container_id=$(docker ps --filter "label=com.docker.compose.project=bats-env-file" --filter "status=running" -q | head -1)
  run docker exec "$container_id" printenv FROM_ENV_FILE
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_equal "env-file-works" "$output"
}

@test "pull policy from compose spec is respected" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pull-policy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pull-policy web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify container is running
  run docker ps --filter "label=com.docker.compose.project=bats-pull-policy" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "pull policy CLI flag overrides compose spec" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pull-policy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pull-policy --pull missing web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify container is running
  run docker ps --filter "label=com.docker.compose.project=bats-pull-policy" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "invalid pull policy is rejected" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pull-policy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pull-policy --pull invalid web
  echo "output: $output"
  echo "status: $status"
  assert_failure
  assert_output_contains "invalid --pull value"
}

@test "pull policy if_not_present maps to missing" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pull-policy-if-not-present"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pull-policy-ifnp web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify container is running
  run docker ps --filter "label=com.docker.compose.project=bats-pull-policy-ifnp" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "pull policy build from compose spec builds and deploys" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pull-policy-build"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pull-policy-build web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify container is running
  run docker ps --filter "label=com.docker.compose.project=bats-pull-policy-build" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "build CLI flag builds and deploys" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pull-policy-build"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pull-policy-build --build --pull never web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify container is running
  run docker ps --filter "label=com.docker.compose.project=bats-pull-policy-build" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "x-healthcheck-wait delays before treating container as healthy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/healthcheck-wait"

  # Record start time
  start_time=$(date +%s)

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-healthcheck-wait web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Record end time
  end_time=$(date +%s)
  elapsed=$((end_time - start_time))

  # The deploy should take at least 3 seconds due to x-healthcheck-wait
  # (we check >= 2 to allow for timing variance)
  if [[ "$elapsed" -lt 2 ]]; then
    flunk "expected deploy to take at least 2s due to x-healthcheck-wait, took ${elapsed}s"
  fi

  # Verify the container is running
  run docker ps --filter "label=com.docker.compose.project=bats-healthcheck-wait" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "x-wait-after-healthy delays after container becomes healthy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/wait-after-healthy"

  # Record start time
  start_time=$(date +%s)

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-wait-after-healthy web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Record end time
  end_time=$(date +%s)
  elapsed=$((end_time - start_time))

  # The deploy should take at least 3 seconds due to x-wait-after-healthy
  # (we check >= 2 to allow for timing variance)
  if [[ "$elapsed" -lt 2 ]]; then
    flunk "expected deploy to take at least 2s due to x-wait-after-healthy, took ${elapsed}s"
  fi

  # Verify the container is running
  run docker ps --filter "label=com.docker.compose.project=bats-wait-after-healthy" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "one-shot service (restart: no) runs to completion" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-success"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-success migrate
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_output_contains "One-shot service migrate completed successfully"
}

@test "one-shot service (restart: no) exit 1 aborts deployment" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-failure"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-failure migrate
  echo "output: $output"
  echo "status: $status"
  assert_failure
}

@test "one-shot service leaves no running containers" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-success"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-success migrate
  echo "output: $output"
  echo "status: $status"
  assert_success

  # No containers should be running (--rm removes them)
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-success" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "" "$output"
}

@test "one-shot pre-deploy runs migrate before web in project deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-pre-deploy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-pre-deploy
  echo "output: $output"
  echo "status: $status"
  assert_success

  # One-shot migrate should have completed
  assert_output_contains "One-shot service migrate completed successfully"

  # Web should be running
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-pre-deploy" --filter "label=com.docker.compose.service=web" --filter "status=running" -q
  echo "web running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"

  # Migrate should have no running containers (--rm removed it)
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-pre-deploy" --filter "label=com.docker.compose.service=migrate" --filter "status=running" -q
  echo "migrate running containers: $output"
  assert_equal "" "$output"
}

@test "one-shot post-deploy runs warm-cache after web in project deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-post-deploy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-post-deploy
  echo "output: $output"
  echo "status: $status"
  assert_success

  # warm-cache one-shot should have completed (check before other run commands overwrite $output)
  assert_output_contains "One-shot service warm-cache completed successfully"

  # Web should be running
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-post-deploy" --filter "label=com.docker.compose.service=web" --filter "status=running" -q
  echo "web running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"

  # warm-cache should have no running containers
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-post-deploy" --filter "label=com.docker.compose.service=warm-cache" --filter "status=running" -q
  echo "warm-cache running containers: $output"
  assert_equal "" "$output"
}

@test "one-shot failure aborts deployment of subsequent services" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-failure-aborts"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-failure-aborts
  echo "output: $output"
  echo "status: $status"
  assert_failure

  # Web should NOT be running (deployment aborted before reaching it)
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-failure-aborts" --filter "label=com.docker.compose.service=web" --filter "status=running" -q
  echo "web running containers: $output"
  assert_equal "" "$output"
}

@test "one-shot without depends_on runs before web without depends_on in project deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-priority"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-priority
  echo "output: $output"
  echo "status: $status"
  assert_success

  # One-shot migrate should have completed
  assert_output_contains "One-shot service migrate completed successfully"

  # Verify ordering: one-shot message should appear before web deployment message
  deploy_output="$output"
  migrate_line=$(echo "$deploy_output" | grep -n "Running one-shot service migrate" | head -1 | cut -d: -f1)
  web_line=$(echo "$deploy_output" | grep -n "Deploying service web" | head -1 | cut -d: -f1)
  if [[ "$migrate_line" -ge "$web_line" ]]; then
    flunk "expected one-shot migrate (line $migrate_line) to run before web deploy (line $web_line)"
  fi

  # Web should be running
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-priority" --filter "label=com.docker.compose.service=web" --filter "status=running" -q
  echo "web running containers: $output"
  assert_equal "1" "$(echo "$output" | wc -l | tr -d ' ')"
}

@test "one-shot service ignores --replicas flag" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-success"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-success --replicas 3 migrate
  echo "output: $output"
  echo "status: $status"
  assert_success

  # One-shot should complete successfully regardless of --replicas
  assert_output_contains "One-shot service migrate completed successfully"

  # No containers should be running (--rm removes them, one-shot ignores replicas)
  run docker ps --filter "label=com.docker.compose.project=bats-one-shot-success" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "" "$output"
}

# =====================================================
# Per-service deploy host command tests
# =====================================================

@test "per-service pre-deploy host command runs before deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pre-deploy-host-command"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pre-deploy-cmd web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # pre-deploy marker should exist
  [ -f /tmp/orch-pre-deploy-started ]
}

@test "per-service post-deploy host command runs after successful deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/post-deploy-host-command"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-post-deploy-cmd web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # post-deploy marker should exist
  [ -f /tmp/orch-post-deploy-completed ]
}

@test "both per-service pre and post deploy host commands run in correct order" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/deploy-host-command-both"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-deploy-both web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # both markers should exist
  [ -f /tmp/orch-deploy-both-pre-time ]
  [ -f /tmp/orch-deploy-both-post-time ]

  pre_time=$(cat /tmp/orch-deploy-both-pre-time)
  post_time=$(cat /tmp/orch-deploy-both-post-time)
  if [[ "$pre_time" -gt "$post_time" ]]; then
    flunk "expected pre-deploy (${pre_time}) to run before post-deploy (${post_time})"
  fi
}

# =====================================================
# Project-level deploy host command tests
# =====================================================

@test "project-level pre-deploy host command runs before deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/project-pre-deploy-host-command"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-project-pre-deploy
  echo "output: $output"
  echo "status: $status"
  assert_success

  # project pre-deploy marker should exist
  [ -f /tmp/orch-project-pre-deploy-started ]
}

@test "project-level post-deploy host command runs after successful deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/project-post-deploy-host-command"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-project-post-deploy
  echo "output: $output"
  echo "status: $status"
  assert_success

  # project post-deploy marker should exist
  [ -f /tmp/orch-project-post-deploy-completed ]
}

@test "both project-level pre and post deploy host commands run in correct order" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/project-deploy-host-command-both"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-project-deploy-both
  echo "output: $output"
  echo "status: $status"
  assert_success

  # both markers should exist
  [ -f /tmp/orch-project-deploy-both-pre-time ]
  [ -f /tmp/orch-project-deploy-both-post-time ]

  pre_time=$(cat /tmp/orch-project-deploy-both-pre-time)
  post_time=$(cat /tmp/orch-project-deploy-both-post-time)
  if [[ "$pre_time" -gt "$post_time" ]]; then
    flunk "expected project pre-deploy (${pre_time}) to run before project post-deploy (${post_time})"
  fi
}

# =====================================================
# Failure behavior tests
# =====================================================

@test "per-service post-deploy host command does NOT run on deploy failure" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/deploy-host-command-failure"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-deploy-failure web
  echo "output: $output"
  echo "status: $status"
  assert_failure

  # post-deploy marker should NOT exist (deploy failed)
  [ ! -f /tmp/orch-deploy-failure-post-deploy ]
}

@test "per-service pre-deploy host command failure aborts deployment" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/pre-deploy-host-command-fails"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-pre-deploy-fails web
  echo "output: $output"
  echo "status: $status"
  assert_failure

  # No containers should be running (deploy was aborted)
  run docker ps --filter "label=com.docker.compose.project=bats-pre-deploy-fails" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "" "$output"
}

@test "project-level post-deploy host command does NOT run on service failure" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/project-deploy-host-command-failure"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-project-deploy-failure
  echo "output: $output"
  echo "status: $status"
  assert_failure

  # project post-deploy marker should NOT exist (service deploy failed)
  [ ! -f /tmp/orch-project-deploy-failure-post-deploy ]
}

@test "project-level pre-deploy host command failure aborts all deployments" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/project-pre-deploy-host-command-fails"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-project-pre-deploy-fails
  echo "output: $output"
  echo "status: $status"
  assert_failure

  # No containers should be running (all deployments aborted)
  run docker ps --filter "label=com.docker.compose.project=bats-project-pre-deploy-fails" --filter "status=running" -q
  echo "running containers: $output"
  assert_equal "" "$output"
}

# =====================================================
# Detached execution tests
# =====================================================

@test "pre-deploy host command detached continues running after exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/detached-pre-deploy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-detached-pre-deploy web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # started marker should exist (command was launched)
  [ -f /tmp/orch-detached-pre-deploy-started ]

  # completed marker should NOT exist yet (sleep 30 in script, deploy finishes faster)
  [ ! -f /tmp/orch-detached-pre-deploy-completed ]
}

@test "post-deploy host command detached does not block deployment" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/detached-post-deploy"

  # Measure deploy time - detached mode should not wait for the sleep 30 in script
  start_time=$(date +%s)
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-detached-post-deploy web
  echo "output: $output"
  echo "status: $status"
  assert_success
  end_time=$(date +%s)

  elapsed=$((end_time - start_time))
  echo "elapsed: ${elapsed}s"

  # Deploy should complete in well under 30 seconds (the detached script has sleep 30)
  # Normal deploy takes ~6-10s for scale-up + healthcheck
  # If detached mode is NOT working (synchronous), it would take 30+ seconds
  if [[ "$elapsed" -ge 25 ]]; then
    flunk "expected deploy to complete in under 25 seconds (detached script has sleep 30), took ${elapsed}s"
  fi
}

@test "post-deploy host command detached process is forked before exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/detached-post-deploy"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-detached-post-deploy-fork web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # The "started" marker must exist — proves the process was forked before exit
  # This is the key test for issue #80: without the fix, this marker may not
  # exist because the goroutine that forks the process may not execute before
  # the main process exits.
  [ -f /tmp/orch-detached-post-deploy-started ]
}

# =====================================================
# Non-detached (synchronous) execution tests
# =====================================================

@test "non-detached deploy commands complete before exit" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/deploy-host-command-non-detached"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-deploy-non-detached web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # both pre-deploy started and completed markers should exist (synchronous)
  [ -f /tmp/orch-deploy-non-detached-pre-started ]
  [ -f /tmp/orch-deploy-non-detached-pre-completed ]

  # both post-deploy started and completed markers should exist (synchronous)
  [ -f /tmp/orch-deploy-non-detached-post-started ]
  [ -f /tmp/orch-deploy-non-detached-post-completed ]
}

# =====================================================
# Template variable tests
# =====================================================

@test "per-service deploy host command template variables expand correctly" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/deploy-host-command-template"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-deploy-template web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # verify service name was expanded
  [ -f /tmp/orch-deploy-template-service ]
  service_name=$(cat /tmp/orch-deploy-template-service | tr -d '[:space:]')
  assert_equal "web" "$service_name"

  # verify project name was expanded
  [ -f /tmp/orch-deploy-template-project ]
  project_name=$(cat /tmp/orch-deploy-template-project | tr -d '[:space:]')
  assert_equal "bats-deploy-template" "$project_name"
}

# =====================================================
# Combined project + per-service tests
# =====================================================

@test "project and per-service deploy hooks run in correct order" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/deploy-host-command-combined"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-deploy-combined
  echo "output: $output"
  echo "status: $status"
  assert_success

  # all four markers should exist
  [ -f /tmp/orch-deploy-combined-project-pre-time ]
  [ -f /tmp/orch-deploy-combined-service-pre-time ]
  [ -f /tmp/orch-deploy-combined-service-post-time ]
  [ -f /tmp/orch-deploy-combined-project-post-time ]

  project_pre=$(cat /tmp/orch-deploy-combined-project-pre-time)
  service_pre=$(cat /tmp/orch-deploy-combined-service-pre-time)
  service_post=$(cat /tmp/orch-deploy-combined-service-post-time)
  project_post=$(cat /tmp/orch-deploy-combined-project-post-time)

  # verify order: project-pre -> service-pre -> service-post -> project-post
  if [[ "$project_pre" -gt "$service_pre" ]]; then
    flunk "expected project-pre (${project_pre}) before service-pre (${service_pre})"
  fi
  if [[ "$service_pre" -gt "$service_post" ]]; then
    flunk "expected service-pre (${service_pre}) before service-post (${service_post})"
  fi
  if [[ "$service_post" -gt "$project_post" ]]; then
    flunk "expected service-post (${service_post}) before project-post (${project_post})"
  fi
}

# =====================================================
# One-shot service deploy hook tests
# =====================================================

@test "one-shot service runs per-service deploy hooks" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/one-shot-deploy-host-command"

  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-one-shot-deploy-cmd migrate
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Both pre and post deploy markers should exist
  [ -f /tmp/orch-one-shot-deploy-pre ]
  [ -f /tmp/orch-one-shot-deploy-post ]

  # One-shot should have completed
  assert_output_contains "One-shot service migrate completed successfully"
}

@test "unchanged service is skipped on second deploy" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/config-hash-skip"

  # First deploy: should proceed normally
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-config-hash web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Verify container is running
  count=$(docker ps -q --filter "label=com.docker.compose.project=bats-config-hash" --filter "label=com.docker.compose.service=web" | wc -l | tr -d ' ')
  assert_equal "1" "$count"

  # Second deploy: should skip because config is unchanged
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-config-hash web
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_output_contains "Skipping unchanged service"
}

@test "force flag overrides config hash skip" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/config-hash-skip"

  # First deploy
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-config-hash-force web
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Second deploy with --force: should NOT skip
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-config-hash-force --force web
  echo "output: $output"
  echo "status: $status"
  assert_success
  [[ "$output" != *"Skipping unchanged service"* ]]
}

@test "project deploy skips unchanged services" {
  cd "${BATS_TEST_DIRNAME}/tests/fixtures/config-hash-skip"

  # First deploy: should proceed normally
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-config-hash-project
  echo "output: $output"
  echo "status: $status"
  assert_success

  # Second deploy: should skip unchanged services
  run "$DOCKER_ORCHESTRATE" deploy --project-name bats-config-hash-project
  echo "output: $output"
  echo "status: $status"
  assert_success
  assert_output_contains "Skipping unchanged service"
}
