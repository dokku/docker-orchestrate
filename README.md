# docker-orchestrate

A Docker CLI plugin to deploy Docker Compose services with support for rolling updates, custom healthchecks, and container naming conventions.

## Features

- Rolling updates with start-first or stop-first strategies.
- Batch processing of container updates with configurable parallelism.
- Script-based healthchecks via Docker Compose extensions.
- Automated container renaming using Go templates.
- Native integration with the Docker CLI.

## Installation

To install as a Docker CLI plugin, build the binary and move it to your Docker plugins directory:

```bash
make install
```

Once installed, the tool is available via `docker orchestrate`.

## Usage

Deploy all services in a compose file:

```bash
docker orchestrate deploy
```

Deploy a specific service:

```bash
docker orchestrate deploy web
```

### Arguments

- `service-name`: The name of a service in the compose file to deploy

### Flags

- `-f, --file`: Path to the Compose configuration file (defaults to `docker-compose.yaml` or `docker-compose.yml`).
- `-p, --project-name`: Specify an alternate project name (defaults to the directory name).
- `--project-directory`: Specify an alternate working directory.
- `--container-name-template`: Go template for container names. Available variables: `.ProjectName`, `.ServiceName`, `.InstanceID`. Default: `{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}`.

## Script Healthchecks

The tool supports an extended healthcheck mechanism via the `x-healthcheck-command` field within the `update_config` section of a service.

```yaml
services:
  web:
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
        x-healthcheck-command: |
          curl -f http://{{.ContainerIP}}:8080/health
```

The script command is treated as a Go template and has access to:

- `.ContainerID`: Full ID of the container.
- `.ContainerShortID`: First 12 characters of the container ID.
- `.ContainerIP`: Internal IP address of the container.
- `.ServiceName`: Name of the service.

The script healthcheck runs after the standard Docker healthcheck (if defined) succeeds.

## Caveats

- **Single-node focus**: This tool is designed for use with Docker Compose on a single Docker Engine. It is not intended for use with Docker Swarm.
- **Script healthcheck locality**: The `x-healthcheck-command` script is executed on the host machine where the `docker orchestrate` command is run, not within the container itself. Use the `HEALTHCHECK` directive to run healthchecks within a container.
- **Network connectivity**: For script healthchecks that rely on `.ContainerIP`, the host machine must have direct network access to the container's IP address (e.g., via the Docker bridge network).
- **Failure Action**: Currently, only the `pause` `failure_action` is supported. Other `failure_action` values will cause this tool to exit non-zero. If a deployment fails, the tool will stop and leave the system in its current state.
