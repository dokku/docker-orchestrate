# docker-orchestrate

A Docker CLI plugin to deploy Docker Compose services with support for rolling updates, custom healthchecks, and container naming conventions.

## Why

Docker Compose is often used as a way to deploy workloads on single servers, but does not natively support rolling restarts, despite [support in the specification](https://docs.docker.com/reference/compose-file/deploy/). This tool aims to fill that gap by implementing the `deploy.update_config` against a locally run `docker compose` project.

## Installation

To install as a Docker CLI plugin, build the binary and move it to your Docker plugins directory:

```bash
make install
```

Once installed, this plugin is available via `docker orchestrate`.

## Usage

Deploy all services in a compose file:

```bash
docker orchestrate deploy
```

Deploy a specific service:

```bash
docker orchestrate deploy web
```

Deploy a service with a specific number of replicas:

```bash
docker orchestrate deploy web --replicas 5
```

Deploy with one or more profiles enabled:

```bash
docker orchestrate deploy --profile production
docker orchestrate deploy --profile production --profile monitoring
docker orchestrate deploy --profile production,monitoring
```

Deploy with multiple compose files (layered):

```bash
docker orchestrate deploy --file docker-compose.yaml --file docker-compose.prod.yaml
docker orchestrate deploy -f docker-compose.yaml -f docker-compose.prod.yaml
```

Deploy with custom environment files (for compose file interpolation):

```bash
docker orchestrate deploy --env-file .env.production
docker orchestrate deploy --env-file .env.production --env-file .env.secrets
```

Deploy while skipping database services:

```bash
docker orchestrate deploy --skip-databases
docker orchestrate deploy web --skip-databases
```

### Arguments

- `service-name`: The name of a service in the compose file to deploy

### Flags

- `--env-file`: Path to an environment variable file for compose file interpolation. Can be specified multiple times. When specified, these files are used instead of the default `.env` file. Environment variables from these files are also available to host scripts and container pre-stop scripts.
- `-f, --file`: Path to a Compose configuration file. Can be specified multiple times to merge files (defaults to `docker-compose.yaml` or `docker-compose.yml`).
- `-p, --project-name`: Specify an alternate project name (defaults to the directory name).
- `--project-directory`: Specify an alternate working directory.
- `--container-name-template`: Go template for container names. Available variables: `.ProjectName`, `.ServiceName`, `.InstanceID`. Default: `{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}`.
- `--profile`: One or more profiles to enable. Can be specified multiple times or as a comma-separated list.
- `--replicas`: Override the number of replicas for a specific service. This flag requires a `service-name` argument.
- `--skip-databases`: Skip deploying database services - as detected by image - when deploying the entire project or a specific service.

## Deployment Order

When deploying a project, services are deployed in the following order:

- If a `web` service exists with no dependencies, it is deployed first
- All other services are deployed in dependency order (dependencies before dependents)
- If the `web` service has dependencies, it follows normal dependency ordering

### Detected Database Services

When using the `--skip-databases` flag, `docker-orchestrate` automatically detects database services by examining the service's image repository. A service is considered a database if its image matches any of the following repositories:

- `clickhouse/clickhouse-server`
- `couchdb` (library/couchdb)
- `elasticsearch` (library/elasticsearch)
- `dokku/docker-grafana-graphite`
- `mariadb`
- `getmeili/meilisearch`
- `memcached` (library/memcached)
- `mongo` (library/mongo)
- `mysql` (library/mysql)
- `nats` (library/nats)
- `omnisci/core-os-cpu`
- `postgres` (library/postgres)
- `fanout/pushpin`
- `rabbitmq` (library/rabbitmq)
- `redis` (library/redis)
- `rethinkdb` (library/rethinkdb)
- `solr` (library/solr)
- `typesense/typesense`

Detection is based on the image repository name (short name), so it works regardless of the image tag or registry. For example, both `postgres:14` and `myregistry.com/library/postgres:latest` would be detected as database services.

### Skipping Services by Label

You can skip individual services by adding the `com.dokku.orchestrate/skip` label with a value of `"true"` to the service definition. This is useful when you want to exclude specific services from deployment without using the `--skip-databases` flag.

```yaml
services:
  web:
    image: nginx:alpine
    labels:
      com.dokku.orchestrate/skip: "true"
  api:
    image: myapp/api:latest
    # This service will be deployed normally
```

When a service has this label set to `"true"`, it will be skipped during deployment.

**Note**: The label value must be exactly the string `"true"` (case-sensitive). Other values like `"false"`, `"yes"`, or `"1"` will not trigger skipping.

### Skipping Model Services

Services that define models (via the `models` field) are automatically skipped during deployment. Model services are typically used for service composition and should not be deployed directly by `docker-orchestrate`.

```yaml
services:
  app:
    models:
      model1:
        # model configuration
  web:
    image: nginx:alpine
    # This service will be deployed normally
```

### Skipping Provider Services

Services that use external providers (defined via the `provider` field) are automatically skipped during deployment. Provider services are typically managed by external systems (like cloud providers) and should not be deployed by `docker-orchestrate`.

```yaml
services:
  database:
    provider:
      type: awesomecloud
      options:
        type: mysql
        foo: bar
  web:
    image: nginx:alpine
    # This service will be deployed normally
```

## Container Stop Grace Period

`docker-orchestrate` respects the compose spec's `stop_grace_period` field when terminating containers during rolling updates, scale-down operations, or service removal. This controls how long Docker waits for a container to stop gracefully before forcefully killing it.

```yaml
services:
  web:
    image: myapp:latest
    stop_grace_period: 30s
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
```

- If `stop_grace_period` is not set, the default timeout is **10 seconds**.
- The value is applied to all container stop operations including rolling updates, scale-down, and service removal.
- For services being removed (no longer in the compose file), the default 10-second timeout is always used.

## Script Extensions

In addition to native healthchecks, `docker-orchestrate` supports extended functionality via custom fields within the `update_config` section of a service.

### Script Healthchecks

The tool supports an extended healthcheck mechanism via the `x-healthcheck-host-command` field.

```yaml
services:
  web:
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
        x-healthcheck-host-command: |
          curl -f http://{{.ContainerIP}}:8080/health
```

The script healthcheck runs after the standard Docker healthcheck (if defined) succeeds.

### Stop Commands

The tool supports several types of stop commands that are executed before and after a container is terminated (e.g., during a rolling update or scale down):

- **`x-pre-stop-host-command`**: Executed on the host before stopping a container
- **`x-pre-stop-command`**: Executed inside the container before stopping (synchronously via Docker SDK)
- **Compose spec `pre_stop` hooks**: Executed inside the container before stopping (via Docker exec API)
- **`x-post-stop-host-command`**: Executed on the host after stopping a container

**Execution order** when stopping a container:

1. `x-pre-stop-host-command` (on the host)
2. `x-pre-stop-command` (script inside the container)
3. Compose spec `pre_stop` hooks (commands inside the container)
4. Container stop + remove
5. `x-post-stop-host-command` (on the host)

```yaml
services:
  web:
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

#### Container Pre-Stop Commands

The `x-pre-stop-command` field allows you to run scripts **inside the container** before it is stopped. This is useful for graceful shutdowns, cleanup tasks, or any operations that need to run within the container's environment.

```yaml
services:
  app:
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

- Scripts are written to `/tmp/pre-stop.sh` inside the container
- Scripts run synchronously - `docker-orchestrate` waits for completion before stopping the container
- Interpreter selection:
  - If the script has a shebang (e.g., `#!/usr/bin/env bash`), that interpreter is used
  - Otherwise, the container's `Config.Shell` property is used:
    - For shell interpreters (sh, bash, dash, ash, zsh, ksh, csh, tcsh, fish): runs with `-c` flag
    - For non-shell interpreters (python, php, etc.): runs the script directly
  - Falls back to `/bin/sh` if neither is available
- Shebang parsing:
  - `#!/usr/bin/env bash` → `/bin/bash -c`
  - `#!/bin/sh` → `/bin/sh -c`
  - `#!/usr/bin/python3` → `/usr/bin/python3`
  - Other shell interpreters are supported based on the shebang with `-c` flag
  - Non-shell interpreters are used directly without `-c`

**Note**: The container must have the following binaries available:

- `/bin/sh` (or the shell specified in `Config.Shell`) - required for script execution
- The interpreter specified in the shebang (if present) - must be available in the container's PATH

#### Compose Spec Pre-Stop Hooks

`docker-orchestrate` supports the compose spec `pre_stop` lifecycle hooks. These hooks are executed inside the container before it is stopped, after any `x-pre-stop-host-command` and `x-pre-stop-command` have completed.

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

Each hook in the `pre_stop` list is executed sequentially via Docker exec. The following fields are supported:

- **`command`**: The command to execute (required). This is a list of strings (command + arguments).
- **`user`**: The user to run the command as (optional).
- **`privileged`**: Whether to run in privileged mode (optional, default: `false`).
- **`working_dir`**: The working directory for the command (optional).
- **`environment`**: Environment variables for the command (optional).

Hooks run synchronously and errors are logged but do not block container stop. If a hook fails, subsequent hooks in the list are skipped.

#### Compose Spec Post-Start Hooks

`docker-orchestrate` supports the compose spec `post_start` lifecycle hooks. These hooks are executed inside the container after it starts, before healthchecks are evaluated. In the rolling update path (`docker compose up`), post_start hooks are handled automatically by the compose CLI. In the scale-up path, `docker-orchestrate` executes them explicitly via Docker exec.

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

Each hook in the `post_start` list is executed sequentially via Docker exec after the container starts. The same fields as `pre_stop` hooks are supported: `command`, `user`, `privileged`, `working_dir`, and `environment`.

If a post_start hook fails, the container is treated as failed: the pre-stop cleanup sequence is executed and the container is terminated, similar to a healthcheck failure.

**Full container lifecycle order** (scale-up path):

1. Container start
2. Compose spec `post_start` hooks (commands inside the container)
3. Docker healthcheck + `x-healthcheck-host-command`
4. _...service runs..._
5. `x-pre-stop-host-command` (on the host)
6. `x-pre-stop-command` (script inside the container)
7. Compose spec `pre_stop` hooks (commands inside the container)
8. Container stop + remove
9. `x-post-stop-host-command` (on the host)

#### Detached Execution

By default, stop commands run synchronously and `docker-orchestrate` waits for them to complete before proceeding. You can configure commands to run in detached mode using `x-pre-stop-host-command-detached` and `x-post-stop-host-command-detached`. When set to `true`, the command runs asynchronously and will continue executing even if `docker-orchestrate` exits.

```yaml
services:
  web:
    deploy:
      update_config:
        x-pre-stop-host-command: |
          # Long-running cleanup task
          ./cleanup-script.sh {{.ContainerID}}
        x-pre-stop-host-command-detached: true
        x-post-stop-host-command: |
          # Send notification asynchronously
          curl -X POST http://monitoring.example.com/notify
        x-post-stop-host-command-detached: true
```

- Detached commands run in the background and do not block deployment
- Detached commands continue running even if `docker-orchestrate` exits or is interrupted
- Only boolean values (`true` or `false`) are allowed
- If not specified, the default is `false` (synchronous execution)
- Invalid values (non-boolean types) will result in an error

### Script Templating

Both `x-healthcheck-host-command`, `x-pre-stop-host-command`, and `x-post-stop-host-command` are treated as Go templates and have access to:

- `.ContainerID`: Full ID of the container.
- `.ContainerShortID`: First 12 characters of the container ID.
- `.ContainerIP`: Internal IP address of the container.
- `.ServiceName`: Name of the service.

### Host Command Shell

Host commands (`x-healthcheck-host-command`, `x-pre-stop-host-command`, `x-post-stop-host-command`) are executed on the host machine using `/bin/sh` by default. To use a different interpreter, add a shebang as the first line of the command:

```yaml
services:
  web:
    deploy:
      update_config:
        x-pre-stop-host-command: |
          #!/usr/bin/env bash
          echo "Using bash-specific features"
          declare -A mymap
```
