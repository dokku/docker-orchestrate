package internal

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	composeTypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
)

// getContainerIP gets the IP address of a container
func getContainerIP(ctx context.Context, client DockerClientInterface, containerID string) (string, error) {
	containerJSON, err := client.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("error inspecting container: %v", err)
	}

	containerIP := ""
	if containerJSON.ContainerJSONBase != nil && containerJSON.HostConfig != nil && containerJSON.HostConfig.NetworkMode.IsHost() {
		containerIP = "127.0.0.1"
	} else if containerJSON.NetworkSettings != nil {
		for networkName, network := range containerJSON.NetworkSettings.Networks {
			if containerJSON.ContainerJSONBase != nil && containerJSON.HostConfig != nil && networkName != containerJSON.HostConfig.NetworkMode.NetworkName() {
				continue
			}
			if network.IPAddress != "" {
				containerIP = network.IPAddress
				break
			}
		}
	}

	return containerIP, nil
}

// isShellInterpreter checks if the given interpreter path is a known shell.
// Accepts full paths (e.g., "/bin/bash") or bare names (e.g., "bash").
func isShellInterpreter(interpreter string) bool {
	knownShells := map[string]bool{
		"sh":   true,
		"bash": true,
		"dash": true,
		"ash":  true,
		"zsh":  true,
		"ksh":  true,
		"csh":  true,
		"tcsh": true,
		"fish": true,
	}
	return knownShells[filepath.Base(interpreter)]
}

// parseShebang parses a shebang line and returns the interpreter command
// Examples:
//   - "#!/usr/bin/env bash" -> ["/bin/bash", "-c"]
//   - "#!/bin/sh" -> ["/bin/sh", "-c"]
//   - "#!/usr/bin/python3" -> ["/usr/bin/python3", "-c"]
//
// Returns empty slice if no shebang is found
func parseShebang(script string) []string {
	lines := strings.Split(script, "\n")
	if len(lines) == 0 {
		return nil
	}

	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "#!") {
		return nil
	}

	// Remove the "#!" prefix
	interpreterLine := strings.TrimPrefix(firstLine, "#!")
	interpreterLine = strings.TrimSpace(interpreterLine)

	// Handle "/usr/bin/env bash" -> "/bin/bash"
	if strings.HasPrefix(interpreterLine, "/usr/bin/env ") {
		interpreter := strings.TrimPrefix(interpreterLine, "/usr/bin/env ")
		// Map common interpreters to their standard paths
		switch interpreter {
		case "bash":
			return []string{"/bin/bash", "-c"}
		case "sh":
			return []string{"/bin/sh", "-c"}
		default:
			if isShellInterpreter(interpreter) {
				return []string{"/usr/bin/env", interpreter, "-c"}
			}
			return []string{"/usr/bin/env", interpreter}
		}
	}

	// Direct path like "/bin/sh" or "/usr/bin/python3"
	parts := strings.Fields(interpreterLine)
	if len(parts) == 0 {
		return nil
	}

	interpreter := parts[0]
	if isShellInterpreter(interpreter) {
		return []string{interpreter, "-c"}
	}
	return []string{interpreter}
}

// ScriptTemplateData is the data structure for the healthcheck command template
type ScriptTemplateData struct {
	// ContainerID is the ID of the container
	ContainerID string
	// ContainerIP is the IP address of the container
	ContainerIP string
	// ContainerShortID is the short ID of the container
	ContainerShortID string
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
}

// runScriptInput is the input for the runHostScript function
type runScriptInput struct {
	// Client is the Docker client to use
	Client DockerClientInterface
	// ContainerID is the ID of the container
	ContainerID string
	// Detached is whether to run the script detached
	Detached bool
	// Env is the environment variables to pass to the script
	Env map[string]string
	// Executor is the command executor to use
	Executor CommandExecutor
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// Script is the script to run
	Script string
	// ScriptType is the type of script
	ScriptType string
	// Shebang is the shebang to use
	Shebang string
}

// runHostScript runs a script on the host
func runHostScript(ctx context.Context, input runScriptInput) error {
	if input.Script == "" {
		return nil
	}

	if input.Client == nil {
		return fmt.Errorf("client is required")
	}

	if input.Executor == nil {
		return fmt.Errorf("executor is required")
	}

	tmpl, err := template.New(input.ScriptType + "-command").Parse(input.Script)
	if err != nil {
		return fmt.Errorf("error parsing %s command template: %v", input.ScriptType, err)
	}

	containerIP, _ := getContainerIP(ctx, input.Client, input.ContainerID)

	containerShortID := input.ContainerID
	if len(containerShortID) > 12 {
		containerShortID = containerShortID[:12]
	}

	var commandBuf bytes.Buffer
	data := ScriptTemplateData{
		ContainerID:      input.ContainerID,
		ContainerIP:      containerIP,
		ContainerShortID: containerShortID,
		ProjectName:      input.ProjectName,
		ServiceName:      input.ServiceName,
	}

	if err := tmpl.Execute(&commandBuf, data); err != nil {
		return fmt.Errorf("error executing %s command template: %v", input.ScriptType, err)
	}

	command := commandBuf.String()
	if input.Shebang == "" {
		input.Shebang = "#!/bin/sh"
	}
	if !strings.HasPrefix(command, "#!") {
		command = input.Shebang + "\n" + command
	}

	tempFile, err := os.CreateTemp("", input.ScriptType+"-*.script")
	if err != nil {
		return fmt.Errorf("error creating temporary %s script: %v", input.ScriptType, err)
	}
	tempFileName := tempFile.Name()

	if _, err := tempFile.WriteString(command); err != nil {
		os.Remove(tempFileName)
		return fmt.Errorf("error writing %s command to temporary file: %v", input.ScriptType, err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFileName)
		return fmt.Errorf("error closing temporary %s file: %v", input.ScriptType, err)
	}

	if err := os.Chmod(tempFileName, 0755); err != nil {
		os.Remove(tempFileName)
		return fmt.Errorf("error making temporary %s script executable: %v", input.ScriptType, err)
	}

	// If detached, run the command asynchronously and return immediately
	if input.Detached {
		go func() {
			// Use background context so the command continues even if the original context is cancelled
			bgCtx := context.Background()
			var output bytes.Buffer
			_, err := input.Executor(bgCtx, ExecCommandInput{
				Command:          tempFileName,
				Env:              input.Env,
				StdoutWriter:     &output,
				StderrWriter:     &output,
				WorkingDirectory: os.TempDir(),
			})
			// In detached mode, we don't report errors - the command runs independently
			_ = err
			// Clean up the temp file after the command completes
			os.Remove(tempFileName)
		}()
		return nil
	}

	// Synchronous execution (default behavior) - clean up temp file when done
	defer os.Remove(tempFileName)

	// Synchronous execution (default behavior)
	var output bytes.Buffer
	_, err = input.Executor(ctx, ExecCommandInput{
		Command:          tempFile.Name(),
		Env:              input.Env,
		StdoutWriter:     &output,
		StderrWriter:     &output,
		WorkingDirectory: os.TempDir(),
	})
	if err != nil {
		return &ErrorWithOutput{
			Err:    fmt.Errorf("%s command failed for container %s: %v", input.ScriptType, containerShortID, err),
			Output: strings.TrimSpace(output.String()),
		}
	}

	return nil
}

// RunContainerScriptInput is the input for the runContainerScript function
type RunContainerScriptInput struct {
	// Client is the Docker client to use
	Client DockerClientInterface
	// ContainerID is the ID of the container
	ContainerID string
	// Env is the environment variables to pass to the container exec
	Env map[string]string
	// Script is the script content to execute
	Script string
	// ScriptPath is the path inside the container where the script will be written
	ScriptPath string
	// ServiceName is the name of the service
	ServiceName string
}

// runContainerScript runs a script inside a container
func runContainerScript(ctx context.Context, input RunContainerScriptInput) error {
	if input.Script == "" {
		return nil
	}

	if input.Client == nil {
		return fmt.Errorf("client is required")
	}

	if input.ScriptPath == "" {
		input.ScriptPath = "/tmp/pre-stop.sh"
	}

	// Inspect container to get Config.Shell
	containerJSON, err := input.Client.ContainerInspect(ctx, input.ContainerID)
	if err != nil {
		return fmt.Errorf("error inspecting container: %v", err)
	}

	// Determine the interpreter
	var cmd []string
	shebang := parseShebang(input.Script)
	if len(shebang) > 0 {
		// Use the interpreter from shebang
		cmd = shebang
	} else {
		// Try container Config.Shell first, then fall back to image Config.Shell
		// Docker does not populate Config.Shell on containers, only on images,
		// so we need to inspect the image to get the SHELL directive.
		var shell []string
		if containerJSON.Config != nil && len(containerJSON.Config.Shell) > 0 {
			shell = containerJSON.Config.Shell
		} else if containerJSON.Image != "" {
			imageJSON, err := input.Client.ImageInspect(ctx, containerJSON.Image)
			if err == nil && imageJSON.Config != nil && len(imageJSON.Config.Shell) > 0 {
				shell = imageJSON.Config.Shell
			}
		}

		if len(shell) > 0 {
			if isShellInterpreter(shell[0]) {
				// Shell interpreter: use [path, "-c"] to avoid double -c
				// when Config.Shell already contains "-c" (e.g., SHELL ["/bin/bash", "-c"])
				cmd = []string{shell[0], "-c"}
			} else {
				// Non-shell interpreter (python, php, etc.): use as-is
				cmd = make([]string, len(shell))
				copy(cmd, shell)
			}
		} else {
			// Fall back to sh
			cmd = []string{"/bin/sh", "-c"}
		}
	}

	// Remove shebang from script if present
	scriptContent := input.Script
	if strings.HasPrefix(strings.TrimSpace(scriptContent), "#!") {
		lines := strings.Split(scriptContent, "\n")
		if len(lines) > 1 {
			scriptContent = strings.Join(lines[1:], "\n")
		} else {
			scriptContent = ""
		}
	}

	// Create a tar archive containing the script
	var tarBuf bytes.Buffer
	tarWriter := tar.NewWriter(&tarBuf)
	defer tarWriter.Close()

	// Get the filename from the script path
	scriptFileName := filepath.Base(input.ScriptPath)

	// Create tar header with executable permissions
	header := &tar.Header{
		Name: scriptFileName,
		Mode: 0755,
		Size: int64(len(scriptContent)),
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		return fmt.Errorf("error writing tar header: %v", err)
	}

	if _, err := tarWriter.Write([]byte(scriptContent)); err != nil {
		return fmt.Errorf("error writing script content to tar: %v", err)
	}

	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("error closing tar writer: %v", err)
	}

	// Get the directory path where the script will be placed
	scriptDir := filepath.Dir(input.ScriptPath)
	if scriptDir == "" || scriptDir == "." {
		scriptDir = "/"
	}

	// Copy the tar archive to the container
	if err := input.Client.CopyToContainer(ctx, input.ContainerID, scriptDir, &tarBuf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("error copying script to container: %v", err)
	}

	// Execute the script using the determined interpreter
	// The script path is passed as an argument to the interpreter
	execCmd := append(cmd, input.ScriptPath)
	var envSlice []string
	for k, v := range input.Env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}
	execConfig := container.ExecOptions{
		Cmd:          execCmd,
		Env:          envSlice,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := input.Client.ContainerExecCreate(ctx, input.ContainerID, execConfig)
	if err != nil {
		return fmt.Errorf("error creating exec instance: %v", err)
	}

	var execOutput bytes.Buffer
	execAttach, err := input.Client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{
		Detach: false,
		Tty:    false,
	})
	if err != nil {
		return fmt.Errorf("error attaching to exec instance: %v", err)
	}
	if execAttach.Conn != nil {
		defer execAttach.Close()
	}

	if err := input.Client.ContainerExecStart(ctx, execID.ID, container.ExecStartOptions{
		Detach: false,
		Tty:    false,
	}); err != nil {
		return fmt.Errorf("error starting exec instance: %v", err)
	}

	// Read the output
	_, _ = io.Copy(&execOutput, execAttach.Reader)

	// Inspect the exec to get the exit code
	execInspect, err := input.Client.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("error inspecting exec instance: %v", err)
	}

	if execInspect.ExitCode != 0 {
		containerShortID := input.ContainerID
		if len(containerShortID) > 12 {
			containerShortID = containerShortID[:12]
		}
		return &ErrorWithOutput{
			Err:    fmt.Errorf("container script failed for container %s: exit code %d", containerShortID, execInspect.ExitCode),
			Output: strings.TrimSpace(execOutput.String()),
		}
	}

	return nil
}

// RunContainerHookInput is the input for the runContainerHook function
type RunContainerHookInput struct {
	// Client is the Docker client to use
	Client DockerClientInterface
	// ContainerID is the ID of the container
	ContainerID string
	// Hook is the compose spec service hook to execute
	Hook composeTypes.ServiceHook
	// ServiceName is the name of the service
	ServiceName string
}

// runContainerHook executes a single compose spec ServiceHook inside a container via Docker exec
func runContainerHook(ctx context.Context, input RunContainerHookInput) error {
	if len(input.Hook.Command) == 0 {
		return nil
	}

	if input.Client == nil {
		return fmt.Errorf("client is required")
	}

	// Convert MappingWithEquals (map[string]*string) to []string ("KEY=VALUE")
	var env []string
	if len(input.Hook.Environment) > 0 {
		keys := make([]string, 0, len(input.Hook.Environment))
		for k := range input.Hook.Environment {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := input.Hook.Environment[k]
			if v != nil {
				env = append(env, fmt.Sprintf("%s=%s", k, *v))
			}
		}
	}

	execConfig := container.ExecOptions{
		Cmd:          []string(input.Hook.Command),
		User:         input.Hook.User,
		Privileged:   input.Hook.Privileged,
		WorkingDir:   input.Hook.WorkingDir,
		Env:          env,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := input.Client.ContainerExecCreate(ctx, input.ContainerID, execConfig)
	if err != nil {
		return fmt.Errorf("error creating exec instance for hook: %v", err)
	}

	var execOutput bytes.Buffer
	execAttach, err := input.Client.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{
		Detach: false,
		Tty:    false,
	})
	if err != nil {
		return fmt.Errorf("error attaching to exec instance for hook: %v", err)
	}
	if execAttach.Conn != nil {
		defer execAttach.Close()
	}

	if err := input.Client.ContainerExecStart(ctx, execID.ID, container.ExecStartOptions{
		Detach: false,
		Tty:    false,
	}); err != nil {
		return fmt.Errorf("error starting exec instance for hook: %v", err)
	}

	// Read the output
	_, _ = io.Copy(&execOutput, execAttach.Reader)

	// Inspect the exec to get the exit code
	execInspect, err := input.Client.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("error inspecting exec instance for hook: %v", err)
	}

	if execInspect.ExitCode != 0 {
		containerShortID := input.ContainerID
		if len(containerShortID) > 12 {
			containerShortID = containerShortID[:12]
		}
		return &ErrorWithOutput{
			Err:    fmt.Errorf("container hook failed for container %s: exit code %d", containerShortID, execInspect.ExitCode),
			Output: strings.TrimSpace(execOutput.String()),
		}
	}

	return nil
}

// RunContainerHooksInput is the input for the runContainerHooks function
type RunContainerHooksInput struct {
	// Client is the Docker client to use
	Client DockerClientInterface
	// ContainerID is the ID of the container
	ContainerID string
	// Hooks is the list of compose spec service hooks to execute
	Hooks []composeTypes.ServiceHook
	// ServiceName is the name of the service
	ServiceName string
}

// runContainerHooks executes a list of compose spec ServiceHooks sequentially inside a container
func runContainerHooks(ctx context.Context, input RunContainerHooksInput) error {
	if len(input.Hooks) == 0 {
		return nil
	}

	for _, hook := range input.Hooks {
		if err := runContainerHook(ctx, RunContainerHookInput{
			Client:      input.Client,
			ContainerID: input.ContainerID,
			Hook:        hook,
			ServiceName: input.ServiceName,
		}); err != nil {
			return err
		}
	}

	return nil
}
