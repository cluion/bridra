package main

import (
	"fmt"
	"io"
	"path"

	"github.com/cluion/bridra/backend/internal/webdist"
)

func buildWebDist(options buildOptions, stdout io.Writer) error {
	digest, err := webdist.Package(options.root, options.metadata.UI.Dist)
	if err != nil {
		return fmt.Errorf("%w: package Web UI: %w", errBuildArtifact, err)
	}
	published := path.Join(webdist.OutputDirectory, digest)
	fmt.Fprintf(stdout, "Artifact: %s/dist\nSHA-256: %s\nManifest: %s/manifest.json\n", published, digest, published)
	return nil
}
