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

## See Also

- [Command Reference](command-reference.md) -- `--pull` and `--build` flag details
- [Deployment Configuration](deployment-configuration.md) -- how config hash comparison interacts with `--build` and `--pull always`
- [Volumes](volumes.md) -- anonymous volume warnings during rolling updates
