# Image Management

## Image Pull Policy

Before deploying a service, docker-orchestrate decides whether to pull the image from a registry. The pull policy controls this behavior. Getting this right matters because pulling always adds deployment time, but skipping pulls means you might deploy an outdated image.

### Pull Policy Resolution

The effective pull policy is determined in this order:

1. If the `--pull` CLI flag is specified, it takes precedence
2. Otherwise, the service's `pull_policy` from the compose file is used
3. If neither is set, the default is `missing`

### Supported Values

| Value | Behavior |
| ------- | ---------- |
| `always` | Always pull the image before deploying. A `docker compose pull` runs before the rolling update to download the image once upfront. |
| `missing` | Only pull if the image is not already present locally. This is the default. |
| `never` | Never pull. The image must already be available locally. |
| `build` | Build the image from a Dockerfile instead of pulling. Sets pull to `never` and runs `docker compose build`. Requires a `build` section in the service definition. |

The compose spec value `if_not_present` is treated as `missing`. The compose spec value `refresh` is not supported and produces an error.

### Examples

Set pull policy in the compose file:

```yaml
services:
  web:
    image: myapp:latest
    pull_policy: always
```

Override from the command line:

```bash
docker orchestrate deploy --pull always
docker orchestrate deploy --pull missing web
```

## Building Images

Use the `--build` flag or `pull_policy: build` to build images from a Dockerfile before deploying. Building is useful in development or CI environments where you want to deploy from source rather than a pre-built image.

```yaml
services:
  web:
    build:
      context: .
      dockerfile: Dockerfile
    pull_policy: build
```

- The `--build` CLI flag triggers `docker compose build` for services with a `build` section
- The `--build` flag is silently ignored for services without a `build` section
- `pull_policy: build` without a `build` section produces an error
- `--build` can be combined with `--pull` -- they are independent concerns
- When building, the pre-pull step is skipped since the image is built locally
- The `--build` flag also bypasses [config hash comparison](deployment-configuration.md#config-hash-comparison) for services with a `build` section, since the image content may have changed

## Cleaning Up Old Images

Every time a service is built, Docker Compose tags the resulting image (`<project>-<service>` by default, or the service's `image:` value) and records `com.docker.compose.project` and `com.docker.compose.service` labels in the image. When you redeploy, the new build takes over the tag and the previous image is left behind -- either untagged (dangling) or, when the image tag changes between deploys, orphaned under its old tag. Over many redeploys these leftover images accumulate and consume disk space.

The `image prune` command reclaims them:

```bash
docker orchestrate image prune
docker orchestrate image prune -p myproject
docker orchestrate image prune --dry-run
```

How it works:

- It keeps the image currently referenced by each service in the compose file (including services disabled by an inactive profile).
- It removes every other image carrying this project's `com.docker.compose.project` label. Because only images built by Compose carry that label, pulled base images and images from other projects are never touched.
- Images still in use by a container -- for example, an old replica draining during a rolling update -- are skipped rather than force-removed.
- Pass `--dry-run` to print which images would be removed without removing them.

The command reads the compose file to determine which images are current, so run it with the same `-f`/`-p`/`--profile` options you deploy with.

### Pruning as part of a deploy

Pass `--prune-images` to `deploy` to prune leftover images automatically after a successful deploy:

```bash
docker orchestrate deploy --build --prune-images
```

Pruning runs only after the deploy succeeds. A prune failure is logged as a warning and does not fail the deploy.

## See Also

- [Command Reference](command-reference.md) -- `--pull`, `--build`, and `image prune` flag details
- [Deployment Configuration](deployment-configuration.md) -- how config hash comparison interacts with `--build` and `--pull always`
- [Volumes](volumes.md) -- anonymous volume warnings during rolling updates
