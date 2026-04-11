package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	dockerClient "github.com/docker/docker/client"
)

// newTestDockerWrapper starts an httptest server with the given handler and
// wires up a DockerClient pointing at it. The server is automatically closed
// via t.Cleanup.
func newTestDockerWrapper(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *DockerClient) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cli, err := dockerClient.NewClientWithOpts(
		dockerClient.WithHost(srv.URL),
		dockerClient.WithVersion("1.43"),
	)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	return srv, &DockerClient{cli: cli}
}

func TestNewDockerClient(t *testing.T) {
	t.Run("valid_host_succeeds", func(t *testing.T) {
		// Port 1 parses but the connection is lazy, so NewDockerClient should
		// succeed without needing a real daemon.
		t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
		cli, err := NewDockerClient()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cli == nil {
			t.Fatal("expected non-nil client")
		}
		if err := cli.Close(); err != nil {
			t.Errorf("close error: %v", err)
		}
	})

	t.Run("malformed_host_returns_error", func(t *testing.T) {
		// No scheme separator — rejected by ParseHostURL.
		t.Setenv("DOCKER_HOST", "malformed-no-scheme")
		_, err := NewDockerClient()
		if err == nil {
			t.Fatal("expected error for malformed DOCKER_HOST")
		}
	})
}

func TestDockerClientContainerList(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/json") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/c1"]}]`))
	})

	containers, err := client.ContainerList(context.Background(), container.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 1 || containers[0].ID != "abc" {
		t.Errorf("unexpected containers: %+v", containers)
	}
}

func TestDockerClientContainerInspect(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/abc/json") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"abc","Name":"/c1","State":{"ExitCode":0},"Config":{}}`))
	})

	info, err := client.ContainerInspect(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ID != "abc" {
		t.Errorf("expected id abc, got %q", info.ID)
	}
}

func TestDockerClientContainerStop(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/containers/abc/stop") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.ContainerStop(context.Background(), "abc", container.StopOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerClientContainerRemove(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || !strings.Contains(r.URL.Path, "/containers/abc") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.ContainerRemove(context.Background(), "abc", container.RemoveOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerClientContainerRename(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/containers/abc/rename") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "newname" {
			t.Errorf("expected name=newname, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.ContainerRename(context.Background(), "abc", "newname"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerClientContainerStart(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/containers/abc/start") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.ContainerStart(context.Background(), "abc", container.StartOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerClientContainerTerminate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		stopCalled := false
		removeCalled := false
		_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.Contains(r.URL.Path, "/stop"):
				stopCalled = true
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodDelete:
				removeCalled = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		})
		if err := client.ContainerTerminate(context.Background(), "abc", 5); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !stopCalled || !removeCalled {
			t.Errorf("expected stop + remove, got stop=%v remove=%v", stopCalled, removeCalled)
		}
	})

	t.Run("stop_error", func(t *testing.T) {
		_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/stop") {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"stop boom"}`))
				return
			}
			t.Errorf("expected only stop call, got %s", r.URL.Path)
		})
		err := client.ContainerTerminate(context.Background(), "abc", 5)
		if err == nil || !strings.Contains(err.Error(), "error stopping container") {
			t.Errorf("expected stop error, got %v", err)
		}
	})

	t.Run("remove_error", func(t *testing.T) {
		_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/stop") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"remove boom"}`))
				return
			}
		})
		err := client.ContainerTerminate(context.Background(), "abc", 5)
		if err == nil || !strings.Contains(err.Error(), "error removing container") {
			t.Errorf("expected remove error, got %v", err)
		}
	})
}

func TestDockerClientImageInspect(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/images/") || !strings.HasSuffix(r.URL.Path, "/json") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"sha256:deadbeef"}`))
	})

	resp, err := client.ImageInspect(context.Background(), "myimage")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "sha256:deadbeef" {
		t.Errorf("expected id sha256:deadbeef, got %q", resp.ID)
	}
}

func TestDockerClientContainerExecCreate(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/abc/exec") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"exec123"}`))
	})

	resp, err := client.ContainerExecCreate(context.Background(), "abc", container.ExecOptions{Cmd: []string{"ls"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "exec123" {
		t.Errorf("expected exec id exec123, got %q", resp.ID)
	}
}

func TestDockerClientContainerExecStart(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/exec/exec123/start") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := client.ContainerExecStart(context.Background(), "exec123", container.ExecStartOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerClientContainerExecAttachError(t *testing.T) {
	// Hijacked attach is hard to fake; return an error so the wrapper line
	// still executes but we don't need to implement the upgrade path.
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	})

	_, err := client.ContainerExecAttach(context.Background(), "exec123", container.ExecAttachOptions{})
	if err == nil {
		t.Fatal("expected error from ExecAttach")
	}
}

func TestDockerClientContainerExecInspect(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/exec/exec123/json") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ID":"exec123","ExitCode":0,"Running":false}`))
	})

	info, err := client.ContainerExecInspect(context.Background(), "exec123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ExecID != "exec123" {
		t.Errorf("expected exec id exec123, got %q", info.ExecID)
	}
}

func TestDockerClientContainerLogs(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/abc/logs") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Non-tty, raw write (no stream header needed for this minimal test).
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("hello logs"))
	})

	rc, err := client.ContainerLogs(context.Background(), "abc", container.LogsOptions{ShowStdout: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rc.Close()
	var sink bytes.Buffer
	if _, err := sink.ReadFrom(rc); err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !strings.Contains(sink.String(), "hello logs") {
		t.Errorf("unexpected body: %q", sink.String())
	}
}

func TestDockerClientCopyToContainer(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.Contains(r.URL.Path, "/containers/abc/archive") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	err := client.CopyToContainer(
		context.Background(),
		"abc",
		"/tmp",
		strings.NewReader("tar-data"),
		container.CopyToContainerOptions{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerClientEvents(t *testing.T) {
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Send one event then let the connection close so the client-side
		// goroutine exits cleanly.
		enc := json.NewEncoder(w)
		_ = enc.Encode(events.Message{Type: "container", Action: "die"})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgCh, errCh := client.Events(ctx, events.ListOptions{})
	if msgCh == nil || errCh == nil {
		t.Fatal("expected non-nil channels from Events")
	}
}

func TestDockerClientClose(t *testing.T) {
	// Close is exercised as part of TestNewDockerClient valid_host_succeeds,
	// but test it explicitly against a live httptest server as well.
	_, client := newTestDockerWrapper(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := client.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}
