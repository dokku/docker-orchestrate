# Models

Docker Compose supports declaring AI models via the `models` field. Services that reference these models are regular container services -- docker-orchestrate deploys them normally and provisions models before any service starts.

## How It Works

A compose file declares models at the project level and references them from services:

```yaml
models:
  llm:
    model: ai/smollm2
    context_size: 2048

services:
  api:
    image: myapp:latest
    models:
      llm:
        endpoint_var: LLM_URL
        model_var: LLM_MODEL
```

Before deploying any services, docker-orchestrate runs a model setup phase:

1. Verifies the Docker Model plugin is installed
2. Lists locally available models via `docker model ls`
3. Pulls any missing models via `docker model pull`
4. Configures all models via `docker model configure` (applies `context_size`, `runtime_flags`)

Models are pulled and configured in parallel when multiple models are declared.

During service deployment, Docker Compose injects environment variables (e.g., `LLM_URL`, `LLM_MODEL`) into services that reference models. This happens automatically when docker-orchestrate runs `docker compose up` for each service.

## Requirements

The [Docker Model plugin](https://docs.docker.com/desktop/features/ai-models/) must be installed when your project declares models. docker-orchestrate verifies this before deploying any services. If the plugin is missing, deployment fails immediately:

```
project references models but the docker-model plugin is not available
```

If the project has no `models` section, the plugin check is skipped entirely.

## Configuration Options

### Project-Level Models

Models are declared at the project level using the `models` field:

```yaml
models:
  llm:
    model: ai/smollm2
    context_size: 2048
    runtime_flags:
      - --threads=4
```

| Field | Description |
|-------|-------------|
| `model` | The model identifier to pull (e.g., `ai/smollm2`) |
| `context_size` | Maximum context window size for the model |
| `runtime_flags` | Additional flags passed to the model runtime |

### Service-Level References

Services reference models by name and specify which environment variables receive the model endpoint and identifier:

```yaml
services:
  api:
    image: myapp:latest
    models:
      llm:
        endpoint_var: LLM_URL
        model_var: LLM_MODEL
```

| Field | Description |
|-------|-------------|
| `endpoint_var` | Environment variable name for the model's API endpoint URL |
| `model_var` | Environment variable name for the model identifier |

## Deployment Behavior

Model configuration always runs during every deployment, regardless of whether individual service configurations have changed. This means changes to project-level model settings like `context_size` or `runtime_flags` take effect on the next deploy without needing `--force`.

Models that are already available locally are not re-pulled. Only missing models are downloaded.

## Error Handling

**Model pull fails**: docker-orchestrate aborts deployment before any services are touched. Re-running `docker orchestrate deploy` retries the pull -- models already pulled in a previous attempt are skipped.

**Service references undefined model**: Docker Compose validates model references during project loading. docker-orchestrate reports the error before deployment starts.

**Model plugin version incompatibility**: If the plugin version does not support `runtime_flags`, they are silently ignored.

## See Also

- [Deployment Configuration](deployment-configuration.md) -- rolling updates, replicas, and deployment order
- [Skipping Services](skipping-services.md) -- services that are excluded from deployment (model services are not skipped)
- [Command Reference](command-reference.md) -- all CLI flags and arguments
