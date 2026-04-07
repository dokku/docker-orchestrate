# Hooks and Scripts

docker-orchestrate supports running commands on the host machine and inside containers before and after deployments and container stop operations. These hooks let you perform tasks like running migrations, notifying monitoring systems, or gracefully draining traffic.

## Deploy Commands

Deploy commands run on the host machine before and after deployments. They exist at two levels: **project-level** (once per deployment invocation) and **per-service** (once per service deployment).

### Project-Level Deploy Commands

Project-level commands bracket the entire deployment. They always run, even when deploying a single service with `docker orchestrate deploy web`.

```yaml
x-pre-deploy-host-command: |
  echo "Deploy starting for {{.ProjectName}}"
x-post-deploy-host-command: |
  curl -X POST https://hooks.example.com/deploy-complete

services:
  web:
    image: myapp:latest
```

- `x-pre-deploy-host-command` runs on the host before any service is deployed
- `x-post-deploy-host-command` runs on the host after all services deploy successfully
- Template variable available: `.ProjectName`

### Per-Service Deploy Commands

Per-service commands bracket an individual service's deployment. They are skipped when a service is skipped due to an unchanged [config hash](deployment-configuration.md#config-hash-comparison).

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      replicas: 3
      x-pre-deploy-host-command: |
        docker compose run --rm migrate
      x-post-deploy-host-command: |
        echo "{{.ServiceName}} deployed in {{.ProjectName}}"
      update_config:
        order: start-first
```

- `x-pre-deploy-host-command` runs on the host before the service deploy starts
- `x-post-deploy-host-command` runs on the host after the service deploys successfully
- Template variables available: `.ServiceName`, `.ProjectName`
- [One-shot services](one-shot-services.md) also receive per-service deploy hooks

### Deploy Command Execution Order

When deploying a project, hooks execute in this order:

1. Project `x-pre-deploy-host-command` (once)
2. For each service in deploy order:
   1. Service `x-pre-deploy-host-command`
   2. Container operations (cleanup, build, pull, rolling update, scale)
   3. Service `x-post-deploy-host-command` (only on success)
3. Project `x-post-deploy-host-command` (once, only if all services succeed)

### Deploy Command Failure Behavior

- If a **pre-deploy** command fails (non-zero exit), the deployment aborts immediately
- **Post-deploy** commands only run after a successful deployment -- they are skipped on failure
- The project-level post-deploy command does not run if any service deployment fails

## Stop Commands

Stop commands run when a container is being terminated, such as during a rolling update or scale-down. They allow you to drain connections, deregister from service discovery, or perform cleanup.

Four types of stop commands execute in this order when stopping a container:

1. `x-pre-stop-host-command` -- runs on the host before stopping
2. `x-pre-stop-command` -- runs as a script inside the container
3. Compose spec `pre_stop` hooks -- run as commands inside the container
4. Container stop + remove
5. `x-post-stop-host-command` -- runs on the host after stopping

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      update_config:
        x-pre-stop-host-command: |
          curl -f http://{{.ContainerIP}}:8080/shutdown
        x-pre-stop-command: |
          #!/bin/sh
          echo "Stopping service gracefully..."
          kill -TERM 1
        x-post-stop-host-command: |
          echo "Container {{.ContainerShortID}} has been stopped"
```

### Container Pre-Stop Commands

The `x-pre-stop-command` field runs a script **inside the container** before it is stopped. This is useful for graceful shutdowns or cleanup tasks that need access to the container's filesystem or processes.

```yaml
services:
  app:
    image: myapp:latest
    deploy:
      update_config:
        x-pre-stop-command: |
          #!/bin/bash
          # Graceful shutdown
          pkill -TERM -P 1
          sleep 2
          # Cleanup
          rm -f /tmp/app.lock
```

How the script runs:

- The script is written to `/tmp/pre-stop.sh` inside the container
- docker-orchestrate waits for it to complete before stopping the container
- If the script has a shebang (e.g., `#!/bin/bash`), that interpreter is used
- Otherwise, the container's `Config.Shell` property is used
- Falls back to `/bin/sh` if neither is available

The container must have `/bin/sh` (or the shell specified in `Config.Shell`) and any interpreter specified in the shebang available.

## Compose Spec Lifecycle Hooks

docker-orchestrate supports the standard compose spec `pre_stop` and `post_start` lifecycle hooks. These run commands inside the container via Docker exec.

### Compose Spec Pre-Stop Hooks

Pre-stop hooks execute inside the container before it is stopped, after any `x-pre-stop-host-command` and `x-pre-stop-command` have completed.

```yaml
services:
  web:
    image: myapp:latest
    pre_stop:
      - command: ["nginx", "-s", "quit"]
      - command: ["sh", "-c", "echo shutting down"]
        user: www-data
        working_dir: /app
        environment:
          GRACEFUL: "true"
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
```

Each hook supports these fields:

| Field | Required | Description |
|-------|----------|-------------|
| `command` | Yes | The command to execute (list of strings) |
| `user` | No | The user to run the command as |
| `privileged` | No | Whether to run in privileged mode (default: `false`) |
| `working_dir` | No | The working directory for the command |
| `environment` | No | Environment variables for the command |

Hooks run sequentially. Errors are logged but do not block the container stop. If a hook fails, subsequent hooks in the list are skipped.

### Compose Spec Post-Start Hooks

Post-start hooks execute inside the container after it starts, before healthchecks are evaluated. In the rolling update path, post-start hooks are handled automatically by the compose CLI. In the scale-up path, docker-orchestrate executes them explicitly.

```yaml
services:
  web:
    image: myapp:latest
    post_start:
      - command: ["sh", "-c", "echo 'Container started'"]
      - command: ["sh", "-c", "/app/setup.sh"]
        user: appuser
        working_dir: /app
        environment:
          INIT_MODE: "true"
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
```

Post-start hooks support the same fields as pre-stop hooks. If a post-start hook fails, the container is treated as failed: the pre-stop cleanup sequence runs and the container is terminated, similar to a healthcheck failure.

## Full Service Lifecycle

The complete order of operations during a project deployment:

1. Project `x-pre-deploy-host-command` (on the host, once)
2. Service `x-pre-deploy-host-command` (on the host, per service)
3. Container operations (per container in rolling update / scale-up):
   1. Container start
   2. Compose spec `post_start` hooks (commands inside the container)
   3. Docker healthcheck (or `x-healthcheck-wait`) + `x-healthcheck-host-command`
   4. `x-wait-after-healthy` delay (if configured)
   5. _...service runs..._
   6. `x-pre-stop-host-command` (on the host)
   7. `x-pre-stop-command` (script inside the container)
   8. Compose spec `pre_stop` hooks (commands inside the container)
   9. Container stop + remove
   10. `x-post-stop-host-command` (on the host)
4. Service `x-post-deploy-host-command` (on the host, per service, only on success)
5. Project `x-post-deploy-host-command` (on the host, once, only if all services succeed)

## Detached Execution

By default, all host commands run synchronously -- docker-orchestrate waits for them to complete before proceeding. You can run commands in detached mode so they execute in the background without blocking the deployment.

Detached mode is available for both deploy commands and stop commands:

```yaml
# Project-level detached deploy commands
x-pre-deploy-host-command: |
  ./notify-deploy-start.sh
x-pre-deploy-host-command-detached: true
x-post-deploy-host-command: |
  ./notify-deploy-complete.sh
x-post-deploy-host-command-detached: true

services:
  web:
    image: myapp:latest
    deploy:
      # Per-service detached deploy commands
      x-pre-deploy-host-command-detached: false
      x-post-deploy-host-command-detached: false
      update_config:
        # Detached stop commands
        x-pre-stop-host-command: |
          ./cleanup-script.sh {{.ContainerID}}
        x-pre-stop-host-command-detached: true
        x-post-stop-host-command: |
          curl -X POST http://monitoring.example.com/notify
        x-post-stop-host-command-detached: true
```

- Detached commands are guaranteed to be started (forked) before docker-orchestrate proceeds
- Detached commands continue running even if docker-orchestrate exits
- Only boolean values (`true` or `false`) are allowed
- Default is `false` (synchronous execution)

## Script Templating

All host commands are Go templates with access to context-specific variables:

| Variable | Description | Available in |
|----------|-------------|-------------|
| `.ContainerID` | Full container ID | Stop commands, healthcheck commands |
| `.ContainerShortID` | First 12 characters of the container ID | Stop commands, healthcheck commands |
| `.ContainerIP` | Internal IP address of the container | Stop commands, healthcheck commands |
| `.ProjectName` | Name of the compose project | All commands |
| `.ServiceName` | Name of the service | Per-service and stop commands |

Container-specific variables (`.ContainerID`, `.ContainerShortID`, `.ContainerIP`) are empty in deploy commands, which run before or after container operations rather than targeting a specific container.

## Host Command Shell

Host commands execute on the host machine using `/bin/sh` by default. To use a different interpreter, add a shebang as the first line:

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      update_config:
        x-pre-stop-host-command: |
          #!/usr/bin/env bash
          echo "Using bash-specific features"
          declare -A mymap
```

## See Also

- [Deployment Configuration](deployment-configuration.md) -- `update_config` fields and deployment order
- [Healthchecks](healthchecks.md) -- health validation that runs between post-start and pre-stop
