package internal

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DotOrchestrateConfig represents the .docker-orchestrate configuration file
type DotOrchestrateConfig struct {
	ComposeFiles []string `yaml:"compose-files"`
	EnvFiles     []string `yaml:"env-files"`
}

// ScanProjects scans a directory for Docker Compose projects with cron-scheduled services.
// Each subdirectory is treated as a potential project. Project compose files are resolved
// in this order:
//  1. .docker-orchestrate YAML file (explicit config)
//  2. .env file with COMPOSE_FILE/COMPOSE_PROJECT_NAME variables
//  3. Default: docker-compose.yml or compose.yml
func ScanProjects(ctx context.Context, configDir string, cliTimezone string) ([]CronProject, error) {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return nil, fmt.Errorf("error reading config directory %s: %v", configDir, err)
	}

	var projects []CronProject
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectDir := filepath.Join(configDir, entry.Name())
		project, err := scanProjectDir(ctx, projectDir, entry.Name(), cliTimezone)
		if err != nil {
			// Log error but continue scanning other projects
			fmt.Fprintf(os.Stderr, "Warning: error scanning project %s: %v\n", entry.Name(), err)
			continue
		}
		if project != nil && len(project.Services) > 0 {
			projects = append(projects, *project)
		}
	}

	return projects, nil
}

// LoadSingleProject loads a single project from compose files
func LoadSingleProject(ctx context.Context, composeFiles []string, envFiles []string, projectName string, cliTimezone string) (*CronProject, error) {
	if len(composeFiles) == 0 {
		return nil, fmt.Errorf("at least one compose file is required")
	}

	project, err := ComposeProject(ctx, projectName, composeFiles, nil, envFiles)
	if err != nil {
		return nil, fmt.Errorf("error loading compose project: %v", err)
	}

	defaults, err := ParseCronDefaults(project.Extensions)
	if err != nil {
		return nil, err
	}

	var services []CronService
	for _, svc := range project.Services {
		if !IsCronService(&svc) {
			continue
		}
		config, err := ParseCronConfig(&svc, defaults, cliTimezone)
		if err != nil {
			return nil, fmt.Errorf("error parsing cron config for service %s: %v", svc.Name, err)
		}
		if config != nil {
			svcCopy := svc
			services = append(services, CronService{
				Name:    svc.Name,
				Config:  config,
				Service: &svcCopy,
			})
		}
	}

	workingDir := filepath.Dir(composeFiles[0])

	return &CronProject{
		Name:             projectName,
		ComposeFiles:     composeFiles,
		EnvFiles:         envFiles,
		WorkingDirectory: workingDir,
		Defaults:         defaults,
		Services:         services,
	}, nil
}

// scanProjectDir scans a single project directory
func scanProjectDir(ctx context.Context, projectDir string, defaultName string, cliTimezone string) (*CronProject, error) {
	composeFiles, envFiles, projectName, err := resolveProjectConfig(projectDir, defaultName)
	if err != nil {
		return nil, err
	}
	if len(composeFiles) == 0 {
		return nil, nil // No compose files found, skip this directory
	}

	return LoadSingleProject(ctx, composeFiles, envFiles, projectName, cliTimezone)
}

// resolveProjectConfig determines the compose files, env files, and project name
// for a given project directory using the resolution order:
// 1. .docker-orchestrate file
// 2. .env with COMPOSE_FILE/COMPOSE_PROJECT_NAME
// 3. Default compose file names
func resolveProjectConfig(projectDir string, defaultName string) (composeFiles []string, envFiles []string, projectName string, err error) {
	projectName = defaultName

	// 1. Check for .docker-orchestrate file
	dotConfigPath := filepath.Join(projectDir, ".docker-orchestrate")
	if _, statErr := os.Stat(dotConfigPath); statErr == nil {
		data, readErr := os.ReadFile(dotConfigPath)
		if readErr != nil {
			return nil, nil, "", fmt.Errorf("error reading %s: %v", dotConfigPath, readErr)
		}

		var config DotOrchestrateConfig
		if yamlErr := yaml.Unmarshal(data, &config); yamlErr != nil {
			return nil, nil, "", fmt.Errorf("error parsing %s: %v", dotConfigPath, yamlErr)
		}

		for _, f := range config.ComposeFiles {
			if !filepath.IsAbs(f) {
				f = filepath.Join(projectDir, f)
			}
			composeFiles = append(composeFiles, f)
		}
		for _, f := range config.EnvFiles {
			if !filepath.IsAbs(f) {
				f = filepath.Join(projectDir, f)
			}
			envFiles = append(envFiles, f)
		}

		if len(composeFiles) > 0 {
			return composeFiles, envFiles, projectName, nil
		}
	}

	// 2. Check .env for COMPOSE_FILE and COMPOSE_PROJECT_NAME
	dotEnvPath := filepath.Join(projectDir, ".env")
	if _, statErr := os.Stat(dotEnvPath); statErr == nil {
		envVars, readErr := parseEnvFile(dotEnvPath)
		if readErr == nil {
			if composeFile, ok := envVars["COMPOSE_FILE"]; ok {
				// COMPOSE_FILE supports : separated paths
				for _, f := range strings.Split(composeFile, ":") {
					f = strings.TrimSpace(f)
					if f == "" {
						continue
					}
					if !filepath.IsAbs(f) {
						f = filepath.Join(projectDir, f)
					}
					composeFiles = append(composeFiles, f)
				}
			}
			if name, ok := envVars["COMPOSE_PROJECT_NAME"]; ok && name != "" {
				projectName = name
			}
		}
	}

	if len(composeFiles) > 0 {
		return composeFiles, envFiles, projectName, nil
	}

	// 3. Fall back to default compose file names
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		candidate := filepath.Join(projectDir, name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			composeFiles = append(composeFiles, candidate)
			break
		}
	}

	return composeFiles, envFiles, projectName, nil
}

// parseEnvFile reads a simple .env file and returns key-value pairs.
// It only handles KEY=VALUE lines (no variable expansion).
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vars := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Remove surrounding quotes
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		vars[key] = value
	}

	return vars, scanner.Err()
}
