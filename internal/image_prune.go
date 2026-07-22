package internal

import (
	"context"
	"fmt"
	"strings"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/josegonzalez/cli-skeleton/command"
)

// PruneImagesInput is the input for the PruneImages function
type PruneImagesInput struct {
	// Client is the Docker client
	Client DockerClientInterface
	// DryRun reports what would be removed without removing anything
	DryRun bool
	// Logger is the logger to use
	Logger *command.ZerologUi
	// Project is the parsed compose project
	Project *composetypes.Project
	// ProjectName is the name of the project
	ProjectName string
}

// PrunedImage describes an image that was (or would be) removed
type PrunedImage struct {
	// ID is the image ID
	ID string
	// Tags is the list of repo tags for the image (empty for dangling images)
	Tags []string
}

// PruneImages removes images left over from previous builds of a compose
// project. Compose bakes a com.docker.compose.project label into every image it
// builds, and that label persists even after a rebuild leaves the old image
// dangling. PruneImages keeps the image currently referenced by each service
// and removes every other image carrying the project's compose label. Images
// still in use by a container are skipped rather than force-removed.
func PruneImages(ctx context.Context, input PruneImagesInput) ([]PrunedImage, error) {
	if input.ProjectName == "" {
		return nil, fmt.Errorf("project name is required")
	}

	if input.Project == nil {
		return nil, fmt.Errorf("project is required")
	}

	// Build the set of image IDs currently referenced by a service. Include
	// profile-disabled services so their images are not removed while a profile
	// is inactive.
	keep := map[string]bool{}
	for _, service := range input.Project.AllServices() {
		imageName := api.GetImageNameOrDefault(service, input.ProjectName)
		if imageName == "" {
			continue
		}
		inspect, err := input.Client.ImageInspect(ctx, imageName)
		if err != nil {
			continue
		}
		keep[inspect.ID] = true
	}

	// List candidate images built by compose for this project. Only built images
	// carry the compose project label, so pulled base images and other projects'
	// images are never considered.
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("com.docker.compose.project=%s", input.ProjectName))
	images, err := input.Client.ImageList(ctx, image.ListOptions{Filters: filterArgs})
	if err != nil {
		return nil, fmt.Errorf("error listing images: %v", err)
	}

	var pruned []PrunedImage
	for _, img := range images {
		if keep[img.ID] {
			continue
		}

		label := imageLabel(img)
		if input.DryRun {
			input.Logger.Info(fmt.Sprintf("Would remove image %s", label))
			pruned = append(pruned, PrunedImage{ID: img.ID, Tags: img.RepoTags})
			continue
		}

		if _, err := input.Client.ImageRemove(ctx, img.ID, image.RemoveOptions{PruneChildren: true}); err != nil {
			input.Logger.Warn(fmt.Sprintf("Skipping image %s: %v", label, err))
			continue
		}

		input.Logger.Info(fmt.Sprintf("Removed image %s", label))
		pruned = append(pruned, PrunedImage{ID: img.ID, Tags: img.RepoTags})
	}

	return pruned, nil
}

// imageLabel returns a human-readable identifier for an image summary,
// preferring its first real tag and falling back to a short image ID.
func imageLabel(img image.Summary) string {
	for _, tag := range img.RepoTags {
		if tag != "" && tag != "<none>:<none>" {
			return tag
		}
	}
	id := strings.TrimPrefix(img.ID, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}
