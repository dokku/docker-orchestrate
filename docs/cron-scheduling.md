# Cron Scheduling

docker-orchestrate includes a built-in cron daemon that runs [one-shot services](one-shot-services.md) on a recurring schedule. Services annotated with the `x-cron` extension are managed by the cron daemon instead of being deployed during `docker orchestrate deploy`.

## Overview

Cron scheduling is designed for tasks that need to run periodically -- report generation, cleanup jobs, batch processing, or scheduled data exports. The cron daemon reads schedules from your compose file, spawns containers at the configured times, and optionally sends webhook notifications when tasks complete.

Key features:

- Standard 5-field and 6-field (with seconds) cron expressions
- Named schedules (`@daily`, `@hourly`, `@weekly`, `@monthly`, `@yearly`, `@annually`)
- `@every` intervals pinned to midnight rather than process start time
- Per-service timezone, timeout, and overlap prevention
- Project-level defaults via `x-cron-defaults`
- Webhook notifications on success, failure, or both
- Multi-project mode for managing many projects from a single daemon
- Automatic config reload (periodic + SIGHUP)
- systemd and runit service installation

## x-cron Extension

Add `x-cron` to any one-shot service (`restart: "no"`) to schedule it:

```yaml
services:
  nightly-report:
    image: myapp:latest
    command: ["./generate-report"]
    restart: "no"
    x-cron:
      schedule: "0 2 * * *"
      timezone: "America/New_York"
      timeout: "30m"
      no-overlap: true
      notify:
        url: "https://hooks.example.com/cron"
        on: "failure"
        include-output: true
```

### Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `schedule` | string | Yes | | Cron expression or named schedule. See [supported expressions](#supported-cron-expressions). |
| `timezone` | string | No | see [resolution order](#timezone-resolution-order) | IANA timezone name (e.g., `America/New_York`, `Europe/London`). |
| `timeout` | string | No | none | Maximum duration for the task (Go duration format: `30m`, `1h`, `2h30m`). |
| `no-overlap` | bool | No | `false` | When `true`, skip the scheduled run if the previous run is still active. |
| `notify` | mapping | No | inherited from `x-cron-defaults` | Webhook notification configuration. See [webhook notifications](#webhook-notifications). |

## x-cron-defaults

Set project-level defaults that apply to all cron services. Per-service `x-cron` fields override these defaults.

```yaml
x-cron-defaults:
  timezone: "UTC"
  notify:
    url: "https://hooks.example.com/cron"
    on: "failure"

services:
  cleanup:
    image: myapp:latest
    command: ["./cleanup"]
    restart: "no"
    x-cron:
      schedule: "@daily"
      # inherits timezone: UTC and notify config from x-cron-defaults

  export:
    image: myapp:latest
    command: ["./export"]
    restart: "no"
    x-cron:
      schedule: "0 */6 * * *"
      timezone: "America/Chicago"  # overrides x-cron-defaults timezone
      notify:
        url: "https://hooks.example.com/export"  # overrides x-cron-defaults notify
        on: "always"
```

## Full Compose File Example

```yaml
x-cron-defaults:
  timezone: "UTC"
  notify:
    url: "https://hooks.example.com/cron"
    on: "failure"

services:
  db:
    image: postgres:16

  web:
    image: myapp:latest
    depends_on:
      db:
        condition: service_healthy

  nightly-report:
    image: myapp:latest
    command: ["./generate-report"]
    restart: "no"
    x-cron:
      schedule: "0 2 * * *"
      timezone: "America/New_York"
      timeout: "30m"
      no-overlap: true

  hourly-sync:
    image: myapp:latest
    command: ["./sync-data"]
    restart: "no"
    x-cron:
      schedule: "@every 1h"
      no-overlap: true

  weekly-cleanup:
    image: myapp:latest
    command: ["./cleanup", "--older-than=30d"]
    restart: "no"
    x-cron:
      schedule: "@weekly"

  migrate:
    image: myapp:latest
    command: ["./migrate", "up"]
    restart: "no"
    depends_on:
      db:
        condition: service_healthy
```

In this example, `docker orchestrate deploy` deploys `db`, `web`, and `migrate` (as a one-shot). The three cron services (`nightly-report`, `hourly-sync`, `weekly-cleanup`) are skipped during deploy and managed by `docker orchestrate cron`.

## Supported Cron Expressions

The scheduler supports the following expression formats:

### Standard 5-Field

```
┌───────── minute (0-59)
│ ┌───────── hour (0-23)
│ │ ┌───────── day of month (1-31)
│ │ │ ┌───────── month (1-12 or JAN-DEC)
│ │ │ │ ┌───────── day of week (0-6 or SUN-SAT)
│ │ │ │ │
* * * * *
```

Examples:
- `0 2 * * *` -- every day at 2:00 AM
- `*/15 * * * *` -- every 15 minutes
- `0 9 * * MON-FRI` -- weekdays at 9:00 AM
- `0 0 1 * *` -- first day of every month at midnight

### 6-Field with Seconds

An optional leading seconds field is supported:

```
┌───────── second (0-59)
│ ┌───────── minute (0-59)
│ │ ┌───────── hour (0-23)
│ │ │ ┌───────── day of month (1-31)
│ │ │ │ ┌───────── month (1-12 or JAN-DEC)
│ │ │ │ │ ┌───────── day of week (0-6 or SUN-SAT)
│ │ │ │ │ │
* * * * * *
```

Example:
- `30 0 * * * *` -- every minute at the 30-second mark

### Named Schedules

| Expression | Equivalent |
|------------|------------|
| `@yearly` | `0 0 1 1 *` |
| `@annually` | `0 0 1 1 *` |
| `@monthly` | `0 0 1 * *` |
| `@weekly` | `0 0 * * 0` |
| `@daily` | `0 0 * * *` |
| `@hourly` | `0 * * * *` |

### @every Intervals

The `@every` expression runs a task at fixed intervals:

```yaml
x-cron:
  schedule: "@every 1h"
```

Supported duration units: `s` (seconds), `m` (minutes), `h` (hours). Combinations like `1h30m` are valid.

**Midnight-pinned behavior:** Unlike standard cron libraries where `@every` is relative to process start time, docker-orchestrate pins `@every` intervals to midnight in the configured timezone. For example, `@every 6h` runs at 00:00, 06:00, 12:00, and 18:00 regardless of when the daemon started. At day boundaries, the schedule wraps to the next day's midnight.

This pinning ensures deterministic run times that do not drift when the daemon restarts.

## Timezone Resolution Order

The timezone for a cron service is resolved in the following order (first match wins):

1. **Per-service** `x-cron.timezone` field
2. **Project-level** `x-cron-defaults.timezone` field
3. **CLI flag** `--timezone` passed to the cron command
4. **System local timezone** if none of the above are set

All timezone values must be valid IANA timezone names (e.g., `America/New_York`, `Europe/London`, `UTC`).

## Commands

### cron

The all-in-one command that runs the scheduler, spawner, and notifier together. This is the recommended way to run the cron daemon.

```bash
docker orchestrate cron -f docker-compose.yml -p myproject
docker orchestrate cron --config-dir /etc/docker-orchestrate
docker orchestrate cron --config-dir /etc/docker-orchestrate --timezone America/New_York
docker orchestrate cron --config-dir /etc/docker-orchestrate --reload-interval 120s
```

### cron run

Runs only the scheduler and spawner without the notifier. Use this when running the notifier as a separate process (see `--split` in [service installation](#service-installation)).

```bash
docker orchestrate cron run -f docker-compose.yml -p myproject
docker orchestrate cron run --config-dir /etc/docker-orchestrate
```

### cron notify

Runs only the notifier (Docker event listener and webhook sender). Listens for container die events from cron-labeled containers, sends webhook notifications, and cleans up containers. Use this alongside `cron run` in split mode.

```bash
docker orchestrate cron notify
```

The notifier has no project-specific flags -- it watches all Docker events globally for containers with the `com.dokku.orchestrate/cron=true` label.

### cron install

Generates and installs init system service configurations for the cron daemon.

```bash
docker orchestrate cron install --init systemd -f docker-compose.yml -p myproject
docker orchestrate cron install --init systemd --config-dir /etc/docker-orchestrate
docker orchestrate cron install --init systemd --split -f docker-compose.yml -p myproject
docker orchestrate cron install --init runit -f docker-compose.yml -p myproject
docker orchestrate cron install --init systemd --dry-run -f docker-compose.yml -p myproject
```

### cron uninstall

Removes installed init system service configurations.

```bash
docker orchestrate cron uninstall --init systemd -p myproject
docker orchestrate cron uninstall --init systemd --split -p myproject
docker orchestrate cron uninstall --init runit -p myproject
docker orchestrate cron uninstall --init systemd --dry-run -p myproject
```

See the [command reference](command-reference.md#cron) for all flags.

## Single Project vs Multi-Project Mode

### Single Project Mode

Use `-f` and `-p` flags to run cron for a single compose file:

```bash
docker orchestrate cron -f docker-compose.yml -p myproject
docker orchestrate cron -f docker-compose.yml -f docker-compose.cron.yml -p myproject
```

### Multi-Project Mode

Use `--config-dir` to manage multiple projects from a single daemon:

```bash
docker orchestrate cron --config-dir /etc/docker-orchestrate
```

You cannot combine single-project flags (`-f`, `-p`) with `--config-dir`.

## Multi-Project Directory Scanning

When using `--config-dir`, docker-orchestrate scans each subdirectory for compose projects. The directory layout looks like:

```
/etc/docker-orchestrate/
  myapp/
    docker-compose.yml
  billing/
    docker-compose.yml
    .env
  reports/
    .docker-orchestrate
    compose.yml
```

Each subdirectory name becomes the default project name. The scanner resolves compose files in this order:

### 1. .docker-orchestrate File

If a `.docker-orchestrate` YAML file is present, it explicitly defines the compose files and env files:

```yaml
compose-files:
  - docker-compose.yml
  - docker-compose.cron.yml
env-files:
  - .env.production
```

Relative paths are resolved from the project subdirectory.

### 2. .env File

If no `.docker-orchestrate` file is found, the scanner checks for a `.env` file containing:

- `COMPOSE_FILE` -- colon-separated list of compose file paths
- `COMPOSE_PROJECT_NAME` -- overrides the directory name as the project name

### 3. Default Compose Files

If neither of the above are found, the scanner looks for the first match among: `docker-compose.yml`, `docker-compose.yaml`, `compose.yml`, `compose.yaml`.

## Service Installation

The `cron install` and `cron uninstall` commands generate init system service configs for systemd and runit.

### systemd (Combined)

```bash
docker orchestrate cron install --init systemd -f /path/to/docker-compose.yml -p myproject
```

Generates a single systemd unit at `/etc/systemd/system/docker-orchestrate-cron-myproject.service`:

```ini
[Unit]
Description=Docker Orchestrate Cron (docker-orchestrate-cron-myproject)
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/path/to/docker-orchestrate cron -f /path/to/docker-compose.yml -p myproject
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
systemctl daemon-reload
systemctl enable --now docker-orchestrate-cron-myproject.service
```

### systemd (Split)

Use `--split` to generate separate services for the scheduler and notifier:

```bash
docker orchestrate cron install --init systemd --split -f /path/to/docker-compose.yml -p myproject
```

This generates two units:
- `docker-orchestrate-cron-myproject-run.service` -- runs `cron run`
- `docker-orchestrate-cron-myproject-notify.service` -- runs `cron notify`

The split mode is useful when running multiple projects that should share a single notifier, since the notifier watches all Docker events globally.

### runit

```bash
docker orchestrate cron install --init runit -f /path/to/docker-compose.yml -p myproject
```

Generates a run script at `/etc/sv/docker-orchestrate-cron-myproject/run`. Enable with:

```bash
ln -s /etc/sv/docker-orchestrate-cron-myproject /etc/service/docker-orchestrate-cron-myproject
```

The `--split` flag works the same way, generating separate `*-run` and `*-notify` services under `/etc/sv/`.

### Dry Run

Use `--dry-run` to preview the generated configuration without writing files:

```bash
docker orchestrate cron install --init systemd --dry-run -f docker-compose.yml -p myproject
```

## Webhook Notifications

When a cron task completes, the notifier can send a JSON webhook to a configured URL.

### Configuration

Webhook notification can be configured per-service or via `x-cron-defaults`:

```yaml
x-cron:
  schedule: "@daily"
  notify:
    url: "https://hooks.example.com/cron"
    on: "failure"
    include-output: true
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `url` | string | Yes | | The webhook endpoint URL. |
| `on` | string | No | `failure` | When to send notifications: `success`, `failure`, or `always`. |
| `include-output` | bool | No | `false` | Include container stdout and stderr in the webhook payload. |

The `x-cron-defaults.notify` block provides a project-level default that applies to all services without their own `notify` configuration.

### Payload Format

The webhook sends a JSON `POST` request with `Content-Type: application/json` and `User-Agent: docker-orchestrate-cron`:

```json
{
  "project": "myproject",
  "service": "nightly-report",
  "schedule": "0 2 * * *",
  "status": "success",
  "exit_code": 0,
  "duration_seconds": 45.2,
  "triggered_at": "2025-01-15T02:00:00Z",
  "completed_at": "2025-01-15T02:00:45Z",
  "container_name": "myproject-nightly-report-20250115-020000-ab3f",
  "stdout": "Report generated successfully\n",
  "stderr": ""
}
```

The `stdout` and `stderr` fields are only present when `include-output` is `true`. The `status` field is `"success"` when exit code is 0, or `"failure"` otherwise.

## Operational Notes

### Container Naming

Cron containers are named with the pattern `<project>-<service>-<YYYYMMDD>-<HHMMSS>-<suffix>`, for example `myproject-nightly-report-20250115-020000-ab3f`. The 4-character random suffix prevents name collisions.

### Labels

Every cron container is tagged with metadata labels:

| Label | Description |
|-------|-------------|
| `com.dokku.orchestrate/cron` | Always `true` -- identifies the container as cron-managed. |
| `com.dokku.orchestrate/cron-project` | The project name. |
| `com.dokku.orchestrate/cron-service` | The service name. |
| `com.dokku.orchestrate/cron-triggered-at` | RFC 3339 timestamp of when the task was triggered. |
| `com.dokku.orchestrate/cron-schedule` | The cron schedule expression. |
| `com.dokku.orchestrate/cron-notify-url` | Webhook URL (only when notifications are configured). |
| `com.dokku.orchestrate/cron-notify-on` | Notification trigger: `success`, `failure`, or `always`. |
| `com.dokku.orchestrate/cron-notify-include-output` | Whether to include stdout/stderr in the webhook payload. |

### Overlap Prevention

When `no-overlap: true` is set, the spawner checks for running containers matching the project and service labels before spawning a new one. If a container from a previous run is still active, the new run is skipped and a log message is emitted.

### Shutdown

On SIGTERM or SIGINT, the cron daemon stops the scheduler immediately and exits. Running cron containers are not stopped -- they continue to completion independently.

### Orphan Recovery

When the notifier starts (or restarts), it scans for exited containers with the `com.dokku.orchestrate/cron=true` label. Containers that exited while the notifier was not running are processed -- notifications are sent if configured, and containers are cleaned up. This ensures no notifications are lost across daemon restarts.

### Config Reload

The scheduler periodically re-reads project configurations at the interval specified by `--reload-interval` (default: `60s`). During a reload, jobs that are no longer present are removed, new jobs are added, and changed jobs are updated.

Send `SIGHUP` to trigger an immediate config reload:

```bash
kill -HUP <pid>
```

### Deploy and Cron Mutual Exclusivity

Services with `x-cron` are automatically skipped during `docker orchestrate deploy`. They are managed exclusively by the cron daemon. Similarly, services without `x-cron` are ignored by the cron daemon.

This means you can keep cron services and long-running services in the same compose file. Deploy handles the long-running services; the cron daemon handles the scheduled tasks.

## See Also

- [One-Shot Services](one-shot-services.md) -- how one-shot services work during deploy
- [Skipping Services](skipping-services.md) -- other mechanisms for skipping services during deploy
- [Command Reference](command-reference.md#cron) -- all cron command flags
