# Deployment Configuration

## Deployment Order

When you run `docker orchestrate deploy` without specifying a service, all services in the compose file are deployed in a specific order. This ordering exists because some services need to be running before others can start (e.g., a database before a web server).

The deployment order is:

1. **One-shot services** with no dependencies run first (e.g., database migrations)
2. If a `web` service exists and has no `depends_on`, it deploys next
3. All remaining services deploy in dependency order -- dependencies before dependents
4. If the `web` service has dependencies, it follows normal dependency ordering

This means a service listed in another service's `depends_on` is always deployed before the service that depends on it.

## Replicas

Replicas let you run multiple instances of the same service. This is useful when a reverse proxy (like Traefik or nginx) load-balances across containers -- during a rolling update, traffic shifts gradually from old containers to new ones.

Set replicas in your compose file:

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      replicas: 3
```

Override replicas from the command line (requires specifying a service name):

```bash
docker orchestrate deploy web --replicas 5
```

## Rolling Update Configuration

The `deploy.update_config` section controls how containers are replaced during a deployment. Without this configuration, docker-orchestrate uses sensible defaults, but tuning these values helps you balance deployment speed against risk.

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
        monitor: 5s
        failure_action: pause
        max_failure_ratio: 0
```

| Field | Default | Description |
| ------- | --------- | ------------- |
| `parallelism` | `1` | Number of containers updated at the same time. A lower number reduces risk -- if something goes wrong, fewer containers are affected. |
| `delay` | `10s` | Time to wait between batches. This gives the system time to stabilize after each batch of containers is replaced. |
| `order` | `start-first` | Determines whether new containers start before or after old ones stop. See [update order](#update-order) below. |
| `monitor` | `5s` | How often docker-orchestrate checks whether a new container is healthy. Also determines the health check timeout (`monitor * 2`). |
| `failure_action` | `pause` | What happens when a container fails its health check: `pause` stops the deployment so you can investigate, `rollback` reverts to the previous containers. |
| `max_failure_ratio` | `0` | The fraction of containers (0 to 1) that can fail before the update stops. `0` means any failure stops the update. `0.3` means up to 30% of containers can fail. |

### Update Order

The `order` field controls the sequence of starting new and stopping old containers. This is the most impactful setting for zero-downtime deployments.

**`start-first`** (recommended for most services): The new container starts and passes health checks before the old container is stopped. This means there is a brief period where both old and new containers are running, and no period where the service is unavailable.

**`stop-first`**: The old container is stopped before the new one starts. This avoids running two versions simultaneously (important if your service cannot tolerate two instances), but creates a brief gap in availability.

## Rollback Configuration

When `failure_action` is set to `rollback`, a failed deployment reverts to the previous container state instead of pausing.

How rollback works depends on the update order:

- **start-first**: New containers that passed health checks are terminated. Old containers were kept running, so they continue serving traffic.
- **stop-first**: Old containers that were stopped (but not removed) are restarted. New containers are terminated.

You can customize rollback behavior with `rollback_config`, which accepts the same fields as `update_config`:

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
        failure_action: rollback
      rollback_config:
        parallelism: 1
        delay: 5s
```

> **Note**: `failure_action: rollback` with `order: stop-first` has a limitation -- if the old container fails to restart during rollback (e.g., the container was removed by an external process), the service may have reduced capacity. Use `order: start-first` for safer rollback behavior.

## Config Hash Comparison

To avoid unnecessary container restarts, docker-orchestrate checks whether a service has actually changed before deploying it. It computes a configuration hash for each service and compares it against the hash stored on running containers (the `com.docker.compose.config-hash` label set by Docker Compose).

A service is **skipped entirely** when all of these are true:

- All running containers have the same config hash as the new configuration
- The number of running containers matches the desired replica count

A service is **scaled only** (no rolling update) when all of these are true:

- All running containers have the same config hash as the new configuration
- The number of running containers does not match the desired replica count

In this case, existing containers are left running and only the delta is acted on -- new containers are added for scale-up, or excess containers are removed for scale-down. This avoids unnecessary disruption when only the replica count changes.

A service receives a **full deploy** (rolling update) when any of these are true:

- It has no running containers (first deploy)
- Any running container has a different config hash
- The `--force` flag is specified
- The `--build` flag is specified and the service has a `build` section
- The pull policy is `always` (via `--pull always` or `pull_policy: always`) and the pulled image differs from the running containers' image

When the pull policy is `always`, the image is pulled first and its digest is compared against the running containers. If the image is identical, the service is still skipped or scaled only as appropriate. This ensures upstream image updates are detected while avoiding unnecessary container cycling when the image has not changed.

When a service is skipped entirely, per-service pre-deploy and post-deploy host commands are also skipped. Project-level hooks always run regardless.

## Container Stop Grace Period

When docker-orchestrate stops a container (during rolling updates, scale-down, or service removal), it sends a stop signal and waits for the container to exit gracefully. The `stop_grace_period` field controls how long to wait before forcefully killing the container.

This matters for services that need to finish processing in-flight requests, close database connections, or flush buffers before shutting down.

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

- If `stop_grace_period` is not set, the default timeout is **10 seconds**
- The value applies to all container stop operations: rolling updates, scale-down, and service removal
- For services being removed (no longer in the compose file), the default 10-second timeout is always used

## See Also

- [Command Reference](command-reference.md) -- all CLI flags including `--force` and `--replicas`
- [Healthchecks](healthchecks.md) -- how health validation interacts with `monitor` and `failure_action`
- [Hooks and Scripts](hooks-and-scripts.md) -- pre/post deploy commands that run around deployments
