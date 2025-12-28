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

Deploy while skipping database services:

```bash
docker orchestrate deploy --skip-databases
docker orchestrate deploy web --skip-databases
```

### Arguments

- `service-name`: The name of a service in the compose file to deploy

### Flags

- `-f, --file`: Path to the Compose configuration file (defaults to `docker-compose.yaml` or `docker-compose.yml`).
- `-p, --project-name`: Specify an alternate project name (defaults to the directory name).
- `--project-directory`: Specify an alternate working directory.
- `--container-name-template`: Go template for container names. Available variables: `.ProjectName`, `.ServiceName`, `.InstanceID`. Default: `{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}`.
- `--profile`: One or more profiles to enable. Can be specified multiple times or as a comma-separated list.
- `--replicas`: Override the number of replicas for a specific service. This flag requires a `service-name` argument.
- `--skip-databases`: Skip deploying database services - as detected by image - when deploying the entire project or a specific service.

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
- **`x-post-stop-host-command`**: Executed on the host after stopping a container

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
  - Otherwise, the container's `Config.Shell` property is used
  - Falls back to `/bin/sh` if neither is available
- Shebang parsing:
  - `#!/usr/bin/env bash` → `/bin/bash -c`
  - `#!/bin/sh` → `/bin/sh -c`
  - `#!/usr/bin/python3` → `/usr/bin/python3 -c`
  - Other interpreters are supported based on the shebang

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

When a service has this label set to `"true"`, it will be skipped during deployment regardless of other skip conditions (such as database detection). The label check takes precedence over the `--skip-databases` flag.

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

Model services are skipped before any other skip checks (provider, labels, or database detection), ensuring they are never deployed regardless of other configuration.

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

Provider services are skipped before skip label and database detection checks (but after model services), ensuring they are never deployed regardless of other configuration.

## Caveats

- **Single-node focus**: `docker orchestrate` is designed for use with Docker Compose on a single Docker Engine. It is not intended for use with Docker Swarm.
- **Script healthcheck locality**: The `x-healthcheck-host-command` script is executed on the host machine where the `docker orchestrate` command is run, not within the container itself. Use the `HEALTHCHECK` directive to run healthchecks within a container.
- **Network connectivity**: For script healthchecks that rely on `.ContainerIP`, the host machine must have direct network access to the container's IP address (e.g., via the Docker bridge network).
- **Failure Action**: Currently, only the `pause` `failure_action` is supported. Other `failure_action` values will cause `docker orchestrate` to exit non-zero. If a deployment fails, `docker orchestrate` will stop and leave the system in its current state.
