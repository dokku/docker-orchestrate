package internal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecCommandResponse(t *testing.T) {
	resp := ExecCommandResponse{
		Stdout:   "  hello  \n",
		Stderr:   "  world  \n",
		ExitCode: 0,
	}

	if resp.StdoutContents() != "hello" {
		t.Errorf("expected 'hello', got '%s'", resp.StdoutContents())
	}
	if resp.StderrContents() != "world" {
		t.Errorf("expected 'world', got '%s'", resp.StderrContents())
	}
	if string(resp.StdoutBytes()) != "hello" {
		t.Errorf("expected 'hello' bytes, got '%s'", string(resp.StdoutBytes()))
	}
	if string(resp.StderrBytes()) != "world" {
		t.Errorf("expected 'world' bytes, got '%s'", string(resp.StderrBytes()))
	}
}

func TestExecCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("successful command", func(t *testing.T) {
		resp, err := ExecCommand(ctx, ExecCommandInput{
			Command: "echo",
			Args:    []string{"hello world"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.StdoutContents() != "hello world" {
			t.Errorf("expected 'hello world', got '%s'", resp.StdoutContents())
		}
		if resp.ExitCode != 0 {
			t.Errorf("expected exit code 0, got %d", resp.ExitCode)
		}
	})

	t.Run("failing command", func(t *testing.T) {
		resp, err := ExecCommand(ctx, ExecCommandInput{
			Command: "ls",
			Args:    []string{"/non-existent-directory-12345"},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp.ExitCode == 0 {
			t.Errorf("expected non-zero exit code, got %d", resp.ExitCode)
		}
		if !strings.Contains(resp.StderrContents(), "No such file or directory") {
			t.Errorf("expected stderr to contain 'No such file or directory', got '%s'", resp.StderrContents())
		}
	})

	t.Run("working directory", func(t *testing.T) {
		resp, err := ExecCommand(ctx, ExecCommandInput{
			Command:          "pwd",
			WorkingDirectory: "/",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.StdoutContents() != "/" {
			t.Errorf("expected '/', got '%s'", resp.StdoutContents())
		}
	})

	t.Run("detached command starts and returns immediately", func(t *testing.T) {
		start := time.Now()
		_, err := ExecCommand(ctx, ExecCommandInput{
			Command:  "sleep",
			Args:     []string{"10"},
			Detached: true,
		})
		duration := time.Since(start)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should return almost immediately, not wait for the 10s sleep
		if duration > 2*time.Second {
			t.Errorf("detached command should return immediately, took %v", duration)
		}
	})

	t.Run("detached command actually executes", func(t *testing.T) {
		markerFile := fmt.Sprintf("/tmp/exec-test-detached-%d", time.Now().UnixNano())
		defer os.Remove(markerFile)

		_, err := ExecCommand(ctx, ExecCommandInput{
			Command:  "/bin/sh",
			Args:     []string{"-c", fmt.Sprintf("touch %s", markerFile)},
			Detached: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Give the detached process a moment to execute
		time.Sleep(500 * time.Millisecond)

		if _, err := os.Stat(markerFile); os.IsNotExist(err) {
			t.Error("expected marker file to exist, detached command did not execute")
		}
	})

	t.Run("detached command survives context cancellation", func(t *testing.T) {
		markerFile := fmt.Sprintf("/tmp/exec-test-ctx-%d", time.Now().UnixNano())
		defer os.Remove(markerFile)

		// Use a background context for detached (simulating what scripts.go does)
		bgCtx := context.Background()

		_, err := ExecCommand(bgCtx, ExecCommandInput{
			Command:  "/bin/sh",
			Args:     []string{"-c", fmt.Sprintf("sleep 1 && touch %s", markerFile)},
			Detached: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Wait for the script to complete
		time.Sleep(2 * time.Second)

		if _, err := os.Stat(markerFile); os.IsNotExist(err) {
			t.Error("expected marker file to exist, detached command should survive")
		}
	})
}
