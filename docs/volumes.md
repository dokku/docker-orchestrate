# Volumes

## Anonymous Volume Warnings

During deployment, docker-orchestrate inspects the service image for `VOLUME` directives defined in the Dockerfile. If any volume path is not mapped to a named volume or bind mount in the compose file, a warning is logged:

```text
Warning: service 'web' has anonymous volume at /var/lib/data — data will be lost during rolling updates. Use a named volume.
```

### Why This Matters

A `VOLUME` directive in a Dockerfile tells Docker to create a storage area at that path. When no named volume is mapped in the compose file, Docker creates an **anonymous volume** -- a volume with a random name that is tied to that specific container.

During a rolling update:

1. Each new container gets a fresh, empty anonymous volume
2. Data from old containers' anonymous volumes is **not** transferred to new containers
3. Old anonymous volumes are removed when docker-orchestrate removes the old container

This means any data written to that path is lost every time a container is replaced.

### How to Fix

Map each image volume path to a named volume in your compose file. Named volumes persist across container recreation:

```yaml
services:
  web:
    image: myapp:latest
    volumes:
      - app-data:/var/lib/data
    deploy:
      update_config:
        order: start-first

volumes:
  app-data:
```

Both named volumes and bind mounts suppress the warning, as both persist data across container recreation.

## See Also

- [Deployment Configuration](deployment-configuration.md) -- rolling updates and how containers are replaced
- [Image Management](image-management.md) -- pull policy and building images
