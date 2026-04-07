# Skipping Services

Not every service in a compose file should be deployed by docker-orchestrate. Database services managed outside of your deployment pipeline, infrastructure services handled by cloud providers, or services used only for composition should be excluded. docker-orchestrate provides several mechanisms to skip services during deployment.

## Database Services

Use the `--skip-databases` flag to skip services that docker-orchestrate identifies as databases. This is useful when your database containers are long-lived and should not be restarted during application deployments.

```bash
docker orchestrate deploy --skip-databases
```

docker-orchestrate detects database services by examining the image repository name. A service is considered a database if its image matches any of the following:

- `clickhouse/clickhouse-server`
- `couchdb`
- `elasticsearch`
- `dokku/docker-grafana-graphite`
- `mariadb`
- `getmeili/meilisearch`
- `memcached`
- `mongo`
- `mysql`
- `nats`
- `omnisci/core-os-cpu`
- `postgres`
- `fanout/pushpin`
- `rabbitmq`
- `redis`
- `rethinkdb`
- `solr`
- `typesense/typesense`

Detection is based on the image repository name (short name), so it works regardless of the image tag or registry. For example, both `postgres:14` and `myregistry.com/library/postgres:latest` are detected as database services.

## Skipping by Label

To skip individual services without relying on image detection, add the `com.dokku.orchestrate/skip` label with a value of `"true"`:

```yaml
services:
  web:
    image: nginx:alpine
    labels:
      com.dokku.orchestrate/skip: "true"
  api:
    image: myapp/api:latest
    # This service deploys normally
```

The label value must be exactly the string `"true"` (case-sensitive). Other values like `"false"`, `"yes"`, or `"1"` do not trigger skipping.

## Provider Services

Services that use external providers (defined via the `provider` field) are automatically skipped during deployment. Provider services are managed by external systems like cloud providers and should not be deployed by docker-orchestrate.

```yaml
services:
  database:
    provider:
      type: awesomecloud
      options:
        type: mysql
        foo: bar
  web:
    image: nginx:alpine
    # This service deploys normally
```

## Cron-Scheduled Services

Services with the `x-cron` extension are automatically skipped during deployment. These services are managed by the cron daemon (`docker orchestrate cron`) and run on their configured schedule rather than during `docker orchestrate deploy`.

```yaml
services:
  nightly-report:
    image: myapp:latest
    command: ["./generate-report"]
    restart: "no"
    x-cron:
      schedule: "0 2 * * *"
  web:
    image: myapp:latest
    # This service deploys normally
```

No flag is needed -- cron-scheduled services are always skipped during deploy. See [Cron Scheduling](cron-scheduling.md) for details.

## See Also

- [Command Reference](command-reference.md) -- the `--skip-databases` flag
- [Cron Scheduling](cron-scheduling.md) -- scheduling one-shot services as recurring cron tasks
- [Deployment Configuration](deployment-configuration.md) -- config hash comparison, another mechanism that skips unchanged services
