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

## See Also

- [Deployment Configuration](deployment-configuration.md) -- how `update_config` controls rolling updates
- [Image Management](image-management.md) -- details on `--pull` and `--build` behavior
- [Skipping Services](skipping-services.md) -- details on `--skip-databases` and other skip mechanisms
