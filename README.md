# docker-orchestrate

A Docker CLI plugin to deploy Docker Compose services with support for rolling updates, custom healthchecks, and container naming conventions.

## Installation

Build and install as a Docker CLI plugin:

```bash
make install
```

Or download a pre-built binary from [GitHub Releases](https://github.com/dokku/docker-orchestrate/releases) and place it in `~/.docker/cli-plugins/`. See the [Getting Started](docs/getting-started.md#installation) guide for details.

Once installed, the plugin is available via `docker orchestrate`.

## Usage

Deploy all services in a compose file:

```bash
docker orchestrate deploy
```

Deploy a specific service:

```bash
docker orchestrate deploy web
```

See the [command reference](docs/command-reference.md) for all flags and options.

## Documentation

- [Getting Started](docs/getting-started.md) -- why docker-orchestrate, installation, and your first deploy
- [Command Reference](docs/command-reference.md) -- all CLI flags and arguments
- [Deployment Configuration](docs/deployment-configuration.md) -- rolling updates, replicas, rollback, and deployment order
- [Healthchecks](docs/healthchecks.md) -- Docker and script-based health validation
- [Hooks and Scripts](docs/hooks-and-scripts.md) -- lifecycle hooks, deploy/stop commands, and templating
- [Image Management](docs/image-management.md) -- pull policy, building images, and volume warnings
- [One-Shot Services](docs/one-shot-services.md) -- migrations, cache warming, and run-to-completion tasks
- [Skipping Services](docs/skipping-services.md) -- excluding databases, labeled services, models, and providers
- [Volumes](docs/volumes.md) -- anonymous volume warnings during rolling updates

## License

[MIT](LICENSE)
