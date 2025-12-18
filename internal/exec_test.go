package internal

import (
	"context"
	"strings"
	"testing"
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
}
