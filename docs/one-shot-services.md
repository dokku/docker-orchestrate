# One-Shot Services

Some tasks need to run once and exit rather than stay running -- database migrations, seed data scripts, or cache warming. docker-orchestrate calls these **one-shot services** and handles them differently from long-running services.

## Detection

A service is treated as one-shot when its `restart` field is set to `"no"` in the compose file:

```yaml
services:
  migrate:
    image: myapp:latest
    command: ["./migrate", "up"]
    restart: "no"
```

## Behavior

One-shot services differ from long-running services in several ways:

- They run via `docker compose run --rm --no-deps <service>`, which starts the container, waits for it to complete, and removes it
- If the service exits with a non-zero code, deployment aborts immediately -- no subsequent services are deployed
- Image pull policy and build flags are respected (images are pulled/built before running)
- Dependency ordering is respected via the standard `depends_on` mechanism
- One-shot services without `depends_on` always run before long-running services (including `web`)
- They do not undergo rolling updates, scaling, healthchecks, or container renaming
- The `--replicas` flag has no effect on one-shot services
- Per-service [deploy hooks](hooks-and-scripts.md#per-service-deploy-commands) still run around one-shot services

## Pre-Deploy One-Shot (Migrations)

A common pattern is running database migrations before deploying your application. Use `depends_on` to express the ordering:

```yaml
services:
  db:
    image: postgres:16
  migrate:
    image: myapp:latest
    command: ["./migrate", "up"]
    restart: "no"
    depends_on:
      db:
        condition: service_healthy
  web:
    image: myapp:latest
    depends_on:
      migrate:
        condition: service_completed_successfully
```

In this example, `migrate` runs as a one-shot after `db` is healthy, and `web` only deploys after `migrate` completes successfully. If the migration fails, `web` is not deployed.

## Post-Deploy One-Shot (Cache Warming)

One-shot services can also run after other services by depending on them:

```yaml
services:
  web:
    image: myapp:latest
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 5s
      timeout: 3s
      retries: 3
  warm-cache:
    image: myapp:latest
    command: ["./warm-cache"]
    restart: "no"
    depends_on:
      web:
        condition: service_healthy
```

Here, `warm-cache` runs after `web`'s deployment completes (including healthchecks and scaling). The `condition` field documents the intent; docker-orchestrate ensures ordering via the dependency graph.

## Cron-Scheduled One-Shot Services

One-shot services with the `x-cron` extension are managed by the cron daemon rather than being deployed during `docker orchestrate deploy`. These services are automatically skipped during deployment and instead run on their configured schedule.

```yaml
services:
  nightly-report:
    image: myapp:latest
    command: ["./generate-report"]
    restart: "no"
    x-cron:
      schedule: "0 2 * * *"
```

See [Cron Scheduling](cron-scheduling.md) for full documentation on scheduling one-shot services as recurring tasks.

## See Also

- [Cron Scheduling](cron-scheduling.md) -- scheduling one-shot services as recurring cron tasks
- [Deployment Configuration](deployment-configuration.md) -- deployment order and how one-shot services fit in
- [Hooks and Scripts](hooks-and-scripts.md) -- per-service deploy commands that also run for one-shot services
