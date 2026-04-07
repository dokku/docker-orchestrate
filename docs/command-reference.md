# Command Reference

## Synopsis

```bash
docker orchestrate deploy [service-name] [flags]
```

## Arguments

| Argument | Required | Description |
|----------|----------|-------------|
| `service-name` | No | The name of a specific service in the compose file to deploy. When omitted, all services are deployed. |

## Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--build` | bool | `false` | Build images before deploying. Runs `docker compose build` before the rolling update and passes `--build` to all compose commands. Only applies to services with a `build` section. |
| `--container-name-template` | string | `{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}` | Go template for container names. See [template variables](#container-name-template-variables). |
| `--env-file` | string (repeatable) | | Path to an environment variable file for compose file interpolation. Replaces the default `.env` file. Can be specified multiple times. |
| `-f, --file` | string (repeatable) | `docker-compose.yaml` | Path to a Compose configuration file. Can be specified multiple times to merge files. |
| `--force` | bool | `false` | Force deploy even if the service configuration is unchanged. See [config hash comparison](deployment-configuration.md#config-hash-comparison). |
| `-p, --project-name` | string | directory name | Specify an alternate project name. |
| `--profile` | string (repeatable) | | One or more profiles to enable. Can be specified multiple times or as a comma-separated list. |
| `--project-directory` | string | | Specify an alternate working directory. |
| `--pull` | string | `missing` | Image pull policy: `always`, `missing`, or `never`. See [image management](image-management.md). |
| `--replicas` | int | from compose file | Override the number of replicas. Requires a `service-name` argument. |
| `--skip-databases` | bool | `false` | Skip deploying database services. See [skipping services](skipping-services.md#database-services). |

## Examples

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
docker orchestrate deploy -f docker-compose.yaml -f docker-compose.prod.yaml
```

Deploy with custom environment files (for compose file interpolation):

```bash
docker orchestrate deploy --env-file .env.production
docker orchestrate deploy --env-file .env.production --env-file .env.secrets
```

Deploy with a specific image pull policy:

```bash
docker orchestrate deploy --pull always
docker orchestrate deploy --pull missing web
```

Build images before deploying:

```bash
docker orchestrate deploy --build
docker orchestrate deploy --build --pull never web
```

Deploy while skipping database services:

```bash
docker orchestrate deploy --skip-databases
```

Force deploy even when service config is unchanged:

```bash
docker orchestrate deploy --force
```

## Container Name Template Variables

The `--container-name-template` flag accepts a Go template with these variables:

| Variable | Description |
|----------|-------------|
| `.ProjectName` | The name of the compose project |
| `.ServiceName` | The name of the service |
| `.InstanceID` | The replica instance number |

The default template produces names like `myproject-web-1`, `myproject-web-2`, etc.

## Run

### run list

List one-shot services (`restart: "no"`) available for on-demand execution.

```bash
docker orchestrate run list [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--env-file` | string (repeatable) | | Path to an environment variable file. Can be specified multiple times. |
| `-f, --file` | string (repeatable) | `docker-compose.yaml` | Path to a Compose configuration file. Can be specified multiple times. |
| `--profile` | string (repeatable) | | One or more profiles to enable. |
| `--project-directory` | string | | Specify an alternate working directory. |
| `-p, --project-name` | string | directory name | Specify an alternate project name. |

```bash
docker orchestrate run list
docker orchestrate run list -f docker-compose.prod.yaml -p myproject
```

### run execute

Run a one-shot service on demand. The service must have `restart: "no"` set. By default, the service runs in the foreground with stdout and stderr streamed to the terminal. Use `--detach` to run in the background.

```bash
docker orchestrate run execute <service-name> [flags]
```

| Argument | Required | Description |
|----------|----------|-------------|
| `service-name` | Yes | The name of the one-shot service to run. |

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--build` | bool | `false` | Build images before running. |
| `--detach` | bool | `false` | Run the service in the background. |
| `--env-file` | string (repeatable) | | Path to an environment variable file. Can be specified multiple times. |
| `-f, --file` | string (repeatable) | `docker-compose.yaml` | Path to a Compose configuration file. Can be specified multiple times. |
| `--profile` | string (repeatable) | | One or more profiles to enable. |
| `--project-directory` | string | | Specify an alternate working directory. |
| `-p, --project-name` | string | directory name | Specify an alternate project name. |
| `--pull` | string | `missing` | Image pull policy: `always`, `missing`, or `never`. |

```bash
docker orchestrate run execute migrate
docker orchestrate run execute seed --build
docker orchestrate run execute export --detach
docker orchestrate run execute migrate -f docker-compose.prod.yaml -p myproject
```

All `run execute` containers are labeled with metadata (`com.dokku.orchestrate/run=true` and project/service/timestamp labels) and persist after exit so they can be queried with `run list-executions`. Per-service [deploy hooks](hooks-and-scripts.md#per-service-deploy-commands) run around the service in foreground mode.

### run list-executions

List past and running one-shot service executions. Queries Docker for containers created by `run execute`.

```bash
docker orchestrate run list-executions [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-p, --project-name` | string | | Filter executions by project name. When omitted, all projects are shown. |

```bash
docker orchestrate run list-executions
docker orchestrate run list-executions -p myproject
```

## Cron

### cron

Run the cron scheduler, spawner, and notifier together. This is the all-in-one command for running scheduled tasks.

```bash
docker orchestrate cron [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--build` | bool | `false` | Build images before running cron tasks. |
| `--config-dir` | string | | Directory to scan for projects (multi-project mode). Cannot be combined with `-f`/`-p`. |
| `--env-file` | string (repeatable) | | Path to an environment variable file for compose file interpolation. Can be specified multiple times. |
| `-f, --file` | string (repeatable) | | Path to a Compose configuration file. Can be specified multiple times to merge files. |
| `-p, --project-name` | string | | The name of the project. |
| `--pull` | string | | Image pull policy: `always`, `missing`, or `never`. |
| `--reload-interval` | string | `60s` | Config reload interval (Go duration format). |
| `--timezone` | string | | Global default timezone for cron schedules (IANA timezone name). |

```bash
docker orchestrate cron -f docker-compose.yml -p myproject
docker orchestrate cron --config-dir /etc/docker-orchestrate
docker orchestrate cron --config-dir /etc/docker-orchestrate --timezone America/New_York
docker orchestrate cron --config-dir /etc/docker-orchestrate --reload-interval 120s
```

### cron run

Run the cron scheduler and spawner without the notifier. Use this with `cron notify` in split mode.

```bash
docker orchestrate cron run [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--build` | bool | `false` | Build images before running cron tasks. |
| `--config-dir` | string | | Directory to scan for projects (multi-project mode). Cannot be combined with `-f`/`-p`. |
| `--env-file` | string (repeatable) | | Path to an environment variable file for compose file interpolation. Can be specified multiple times. |
| `-f, --file` | string (repeatable) | | Path to a Compose configuration file. Can be specified multiple times to merge files. |
| `-p, --project-name` | string | | The name of the project. |
| `--pull` | string | | Image pull policy: `always`, `missing`, or `never`. |
| `--reload-interval` | string | `60s` | Config reload interval (Go duration format). |
| `--timezone` | string | | Global default timezone for cron schedules (IANA timezone name). |

```bash
docker orchestrate cron run -f docker-compose.yml -p myproject
docker orchestrate cron run --config-dir /etc/docker-orchestrate
```

### cron notify

Run the cron notifier (Docker event listener and webhook sender). Listens for container die events from cron-labeled containers and sends webhook notifications. Has no project-specific flags -- it watches all Docker events globally.

```bash
docker orchestrate cron notify
```

### cron install

Generate and install init system service configs for the cron scheduler.

```bash
docker orchestrate cron install [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--config-dir` | string | | Directory to scan for projects (multi-project mode). |
| `--dry-run` | bool | `false` | Print generated configs without installing. |
| `--env-file` | string (repeatable) | | Path to an environment variable file. Can be specified multiple times. |
| `-f, --file` | string (repeatable) | | Path to a Compose configuration file. Can be specified multiple times. |
| `--init` | string | `systemd` | Init system to generate configs for: `systemd` or `runit`. |
| `-p, --project-name` | string | | The name of the project. |
| `--split` | bool | `false` | Generate separate services for the scheduler and notifier. |

```bash
docker orchestrate cron install --init systemd -f docker-compose.yml -p myproject
docker orchestrate cron install --init systemd --split -f docker-compose.yml -p myproject
docker orchestrate cron install --init runit -f docker-compose.yml -p myproject
docker orchestrate cron install --init systemd --dry-run -f docker-compose.yml -p myproject
docker orchestrate cron install --init systemd --config-dir /etc/docker-orchestrate
```

### cron uninstall

Remove installed init system service configs for the cron scheduler.

```bash
docker orchestrate cron uninstall [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dry-run` | bool | `false` | Print what would be removed without removing. |
| `--include-notify` | bool | `false` | Also remove the shared notifier service (split mode only). |
| `--init` | string | `systemd` | Init system to remove configs for: `systemd` or `runit`. |
| `-p, --project-name` | string | | The name of the project. |
| `--split` | bool | `false` | Remove separate scheduler and notifier services. |

```bash
docker orchestrate cron uninstall --init systemd -p myproject
docker orchestrate cron uninstall --init systemd --split -p myproject
docker orchestrate cron uninstall --init systemd --split --include-notify -p myproject
docker orchestrate cron uninstall --init runit -p myproject
docker orchestrate cron uninstall --init systemd --dry-run -p myproject
```

## See Also

- [Cron Scheduling](cron-scheduling.md) -- full cron scheduling documentation
- [Deployment Configuration](deployment-configuration.md) -- how `update_config` controls rolling updates
- [Image Management](image-management.md) -- details on `--pull` and `--build` behavior
- [One-Shot Services](one-shot-services.md) -- one-shot service behavior and manual execution
- [Skipping Services](skipping-services.md) -- details on `--skip-databases` and other skip mechanisms
