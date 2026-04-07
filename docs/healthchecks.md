# Healthchecks

During a rolling update, docker-orchestrate replaces containers one batch at a time. Before proceeding to the next batch, it checks that each new container is healthy. Without healthchecks, a container is considered healthy the moment it starts running -- which may be before your application has finished initializing. Healthchecks prevent traffic from being routed to containers that are not yet ready.

## Docker Healthchecks

The most common approach is to define a healthcheck directly in your compose file. Docker periodically runs the specified command inside the container, and docker-orchestrate waits for it to report healthy before continuing the deployment.

```yaml
services:
  web:
    image: myapp:latest
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 5s
      timeout: 3s
      retries: 3
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
        monitor: 5s
```

The `monitor` field in `update_config` controls how often docker-orchestrate polls the container's health status. It also determines the health check timeout: `monitor * 2`. If a container does not become healthy within that timeout, it is considered failed.

## Script Healthchecks

For cases where you need to validate health from outside the container (e.g., checking an HTTP endpoint from the host, verifying service registration in a load balancer), use the `x-healthcheck-host-command` extension.

This command runs **on the host machine**, not inside the container. It runs after the Docker healthcheck (if defined) succeeds.

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
        x-healthcheck-host-command: |
          curl -f http://{{.ContainerIP}}:8080/health
```

The command is a Go template with access to [template variables](hooks-and-scripts.md#script-templating). The most commonly used variable here is `.ContainerIP`, which resolves to the container's internal IP address.

If the script exits with a non-zero code, the container is considered unhealthy.

## Healthcheck Wait

Some containers do not define a Docker healthcheck but still need time to start up before they can serve traffic. The `x-healthcheck-wait` extension specifies a duration to wait before treating a container without a healthcheck as healthy.

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
        x-healthcheck-wait: "10s"
```

- The value is a Go duration string (e.g., `"5s"`, `"500ms"`, `"1m30s"`)
- This only applies to containers **without** a Docker healthcheck. Containers with a healthcheck use the standard health status polling instead.
- The container must be continuously running for the full duration. If the container restarts during the wait, the timer resets.
- When `x-healthcheck-wait` is set, the health check timeout becomes `(monitor + x-healthcheck-wait) * 2`, giving enough room for polling plus the wait
- The wait is applied after the container reaches the running state but before the `x-healthcheck-host-command` (if any) is executed

## Wait After Healthy

The `x-wait-after-healthy` extension adds a delay after a container becomes healthy before proceeding with the deployment (e.g., stopping the old container).

This is useful when a reverse proxy like Traefik or nginx-proxy needs time to discover the new container and update its routing configuration. Without this delay, the old container might be removed before the proxy starts sending traffic to the new one, resulting in dropped requests.

```yaml
services:
  web:
    image: myapp:latest
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        order: start-first
        x-wait-after-healthy: "5s"
```

- The value is a Go duration string (e.g., `"5s"`, `"500ms"`, `"1m30s"`)
- Applies regardless of whether a Docker healthcheck is defined -- the delay happens after all healthcheck validation passes (including `x-healthcheck-host-command` if configured)
- The delay is applied per-container, after the healthcheck passes but before the old container's stop sequence begins

## Healthcheck Flow

When a new container starts during a rolling update, the full health validation sequence is:

1. Container starts and enters the running state
2. [Compose spec `post_start` hooks](hooks-and-scripts.md#compose-spec-post-start-hooks) execute (if defined)
3. Docker healthcheck polling begins (or `x-healthcheck-wait` timer starts if no healthcheck is defined)
4. `x-healthcheck-host-command` runs (if defined, after Docker healthcheck passes)
5. `x-wait-after-healthy` delay (if configured)
6. Container is considered ready -- deployment proceeds to the next step

If any step fails, the container is treated as unhealthy and the configured `failure_action` determines what happens next.

## See Also

- [Deployment Configuration](deployment-configuration.md) -- `monitor`, `failure_action`, and other `update_config` fields
- [Hooks and Scripts](hooks-and-scripts.md) -- the full service lifecycle and template variables for `x-healthcheck-host-command`
