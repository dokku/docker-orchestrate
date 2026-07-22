package internal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/image"
)

// pruneTestProject returns a project with a build service (web, defaulting to
// the <project>-web image) and a service with an explicit image (api).
func pruneTestProject() *types.Project {
	return &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{Name: "web"},
			"api": types.ServiceConfig{Name: "api", Image: "myapp:latest"},
		},
	}
}

// pruneInspectByName maps resolved image names to IDs for the keep-set lookup.
func pruneInspectByName(ids map[string]string) func(ctx context.Context, imageID string) (image.InspectResponse, error) {
	return func(ctx context.Context, imageID string) (image.InspectResponse, error) {
		if id, ok := ids[imageID]; ok {
			return image.InspectResponse{ID: id}, nil
		}
		return image.InspectResponse{}, fmt.Errorf("no such image: %s", imageID)
	}
}

func TestPruneImagesRemovesLeftovers(t *testing.T) {
	logger, buf := testLogger()

	var removed []string
	mockClient := &mockDockerClient{
		imageInspect: pruneInspectByName(map[string]string{
			"myproject-web": "sha256:web-current",
			"myapp:latest":  "sha256:api-current",
		}),
		imageList: func(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
			if got := options.Filters.Get("label"); len(got) != 1 || got[0] != "com.docker.compose.project=myproject" {
				t.Errorf("expected project label filter, got %v", got)
			}
			return []image.Summary{
				{ID: "sha256:web-current", RepoTags: []string{"myproject-web:latest"}},
				{ID: "sha256:web-old", RepoTags: []string{"<none>:<none>"}},
				{ID: "sha256:api-current", RepoTags: []string{"myapp:latest"}},
				{ID: "sha256:api-old", RepoTags: []string{"myapp:v1"}},
			}, nil
		},
		imageRemove: func(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			removed = append(removed, imageID)
			return []image.DeleteResponse{{Deleted: imageID}}, nil
		},
	}

	pruned, err := PruneImages(context.Background(), PruneImagesInput{
		Client:      mockClient,
		Logger:      logger,
		Project:     pruneTestProject(),
		ProjectName: "myproject",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pruned) != 2 {
		t.Fatalf("expected 2 pruned images, got %d (%+v)", len(pruned), pruned)
	}

	wantRemoved := map[string]bool{"sha256:web-old": true, "sha256:api-old": true}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removals, got %v", removed)
	}
	for _, id := range removed {
		if !wantRemoved[id] {
			t.Errorf("unexpected removal of %s", id)
		}
	}
	for _, id := range removed {
		if id == "sha256:web-current" || id == "sha256:api-current" {
			t.Errorf("current image %s must not be removed", id)
		}
	}

	out := buf.String()
	if !strings.Contains(out, "Removed image") {
		t.Errorf("expected removal log output, got %q", out)
	}
}

func TestPruneImagesSkipsInUseImage(t *testing.T) {
	logger, buf := testLogger()

	mockClient := &mockDockerClient{
		imageInspect: pruneInspectByName(map[string]string{}),
		imageList: func(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
			return []image.Summary{
				{ID: "sha256:inuse", RepoTags: []string{"<none>:<none>"}},
			}, nil
		},
		imageRemove: func(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			return nil, fmt.Errorf("conflict: image is being used by running container abc123")
		},
	}

	pruned, err := PruneImages(context.Background(), PruneImagesInput{
		Client:      mockClient,
		Logger:      logger,
		Project:     pruneTestProject(),
		ProjectName: "myproject",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("expected no pruned images, got %+v", pruned)
	}
	if out := buf.String(); !strings.Contains(out, "Skipping image") {
		t.Errorf("expected skip warning in log, got %q", out)
	}
}

func TestPruneImagesDryRun(t *testing.T) {
	logger, buf := testLogger()

	mockClient := &mockDockerClient{
		imageInspect: pruneInspectByName(map[string]string{}),
		imageList: func(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
			return []image.Summary{
				{ID: "sha256:leftover", RepoTags: []string{"<none>:<none>"}},
			}, nil
		},
		imageRemove: func(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			t.Fatalf("ImageRemove must not be called in dry-run mode")
			return nil, nil
		},
	}

	pruned, err := PruneImages(context.Background(), PruneImagesInput{
		Client:      mockClient,
		DryRun:      true,
		Logger:      logger,
		Project:     pruneTestProject(),
		ProjectName: "myproject",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pruned) != 1 || pruned[0].ID != "sha256:leftover" {
		t.Errorf("expected leftover to be reported, got %+v", pruned)
	}
	if out := buf.String(); !strings.Contains(out, "Would remove image") {
		t.Errorf("expected dry-run log, got %q", out)
	}
}

func TestPruneImagesNoCandidates(t *testing.T) {
	logger, _ := testLogger()

	mockClient := &mockDockerClient{
		imageInspect: pruneInspectByName(map[string]string{}),
		imageList: func(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
			return nil, nil
		},
		imageRemove: func(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			t.Fatalf("ImageRemove must not be called with no candidates")
			return nil, nil
		},
	}

	pruned, err := PruneImages(context.Background(), PruneImagesInput{
		Client:      mockClient,
		Logger:      logger,
		Project:     pruneTestProject(),
		ProjectName: "myproject",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("expected no pruned images, got %+v", pruned)
	}
}

func TestPruneImagesValidation(t *testing.T) {
	logger, _ := testLogger()

	tests := []struct {
		name        string
		input       PruneImagesInput
		expectedErr string
	}{
		{
			name:        "missing project name",
			input:       PruneImagesInput{Client: &mockDockerClient{}, Logger: logger, Project: pruneTestProject()},
			expectedErr: "project name is required",
		},
		{
			name:        "nil project",
			input:       PruneImagesInput{Client: &mockDockerClient{}, Logger: logger, ProjectName: "myproject"},
			expectedErr: "project is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PruneImages(context.Background(), tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("expected error containing %q, got %v", tt.expectedErr, err)
			}
		})
	}
}

func TestPruneImagesListError(t *testing.T) {
	logger, _ := testLogger()

	mockClient := &mockDockerClient{
		imageInspect: pruneInspectByName(map[string]string{}),
		imageList: func(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
			return nil, fmt.Errorf("daemon unavailable")
		},
	}

	_, err := PruneImages(context.Background(), PruneImagesInput{
		Client:      mockClient,
		Logger:      logger,
		Project:     pruneTestProject(),
		ProjectName: "myproject",
	})
	if err == nil || !strings.Contains(err.Error(), "error listing images") {
		t.Errorf("expected list error, got %v", err)
	}
}
