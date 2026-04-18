# Getting Started

## Why docker-orchestrate

Docker Compose is often used to deploy workloads on single servers, but it does not natively support rolling restarts -- despite [support in the specification](https://docs.docker.com/reference/compose-file/deploy/). When you run `docker compose up`, all containers are recreated at once, causing downtime.

docker-orchestrate fills this gap by reading the `deploy.update_config` section of your compose file and performing rolling updates: replacing containers one at a time so your service stays available throughout the deployment.

## Installation

Once installed, the plugin is available as `docker orchestrate`:

```bash
docker orchestrate version
```

### Quick Install (Linux and macOS)

Use the install script to download the latest release and install it as a Docker CLI plugin:

```bash
curl -fsSL https://raw.githubusercontent.com/dokku/docker-orchestrate/main/install.sh | sh
```

To install a specific version:

```bash
VERSION=0.1.0 curl -fsSL https://raw.githubusercontent.com/dokku/docker-orchestrate/main/install.sh | sh
```

To install to a custom directory:

```bash
PLUGIN_DIR=/usr/libexec/docker/cli-plugins curl -fsSL https://raw.githubusercontent.com/dokku/docker-orchestrate/main/install.sh | sh
```

### Homebrew (macOS)

```bash
brew install dokku/repo/docker-orchestrate
```

### Debian/Ubuntu

```bash
sudo apt-get update
sudo apt-get install docker-orchestrate
```

The Debian package installs the binary to both `/usr/bin/docker-orchestrate` (for direct invocation) and `/usr/libexec/docker/cli-plugins/docker-orchestrate` (for automatic Docker CLI plugin discovery).

### Binary Download

Download a pre-built binary from [GitHub Releases](https://github.com/dokku/docker-orchestrate/releases) and place it in your Docker CLI plugins directory:

```bash
mkdir -p ~/.docker/cli-plugins
cp docker-orchestrate ~/.docker/cli-plugins/docker-orchestrate
chmod +x ~/.docker/cli-plugins/docker-orchestrate
```

Docker looks for plugins in these directories:

- `~/.docker/cli-plugins/` (per-user)
- `/usr/libexec/docker/cli-plugins/` (system-wide)

### From Source

Build and install as a Docker CLI plugin:

```bash
make install
```

This builds the binary for your platform and copies it to `~/.docker/cli-plugins/docker-orchestrate`.

## Prerequisites

- Docker Engine with the Docker Compose v2 plugin
- A `docker-compose.yaml` file defining your services

## Your First Deploy

Given a compose file with a web service:

```yaml
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
```

Deploy it:

```bash
docker orchestrate deploy
```

docker-orchestrate creates the container, names it according to the [container name template](command-reference.md#container-name-template-variables) (e.g., `myproject-web-1`), and reports the result.

## Deploying a Specific Service

When your compose file has multiple services, you can deploy just one:

```bash
docker orchestrate deploy web
```

This deploys only the `web` service. [Project-level hooks](hooks-and-scripts.md#project-level-deploy-commands) still run.

## Deploying with Replicas

Run multiple instances of a service to enable rolling updates with zero downtime. During an update, docker-orchestrate replaces containers one at a time so at least one instance is always running:

```bash
docker orchestrate deploy web --replicas 3
```

Or set replicas in the compose file:

```yaml
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    deploy:
      replicas: 3
```

## How Rolling Updates Work

When you deploy a service that already has running containers, docker-orchestrate performs a **rolling update** rather than replacing all containers at once. The update strategy is controlled by `deploy.update_config` in the compose file.

With the default **start-first** order:

1. A new container starts alongside the existing ones
2. docker-orchestrate waits for the new container to pass [healthchecks](healthchecks.md) (if configured)
3. Once healthy, the old container is stopped and removed
4. This repeats for each container, one batch at a time

This means your service is never fully down -- old containers continue serving traffic until new ones are ready.

With **stop-first** order, the old container is stopped before the new one starts. This avoids running two versions simultaneously but creates a brief gap in availability.

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        delay: 10s
        order: start-first
```

For full details on all update_config fields, see [Deployment Configuration](deployment-configuration.md#rolling-update-configuration).

## Next Steps

- [Command Reference](command-reference.md) -- all CLI flags and arguments
- [Deployment Configuration](deployment-configuration.md) -- tune rolling update behavior, rollback, and config hash comparison
- [Healthchecks](healthchecks.md) -- validate containers are ready before routing traffic
- [Hooks and Scripts](hooks-and-scripts.md) -- run commands before and after deployments
- [Image Management](image-management.md) -- control image pulling and building
- [One-Shot Services](one-shot-services.md) -- run migrations and other tasks during deployment
- [Skipping Services](skipping-services.md) -- exclude services from deployment
