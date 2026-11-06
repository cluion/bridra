package main

import (
	"fmt"
	"io"
	"path"

	"github.com/cluion/bridra/backend/internal/webdesktop"
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

func buildWebDesktop(options buildOptions, stdout io.Writer) error {
	digest := options.webDistSHA256
	if digest == "" {
		var err error
		digest, err = webdist.Package(options.root, options.metadata.UI.Dist)
		if err != nil {
			return fmt.Errorf("%w: package Web UI: %w", errBuildArtifact, err)
		}
	}
	artifact, err := webdesktop.Build(webdesktop.Config{
		Root: options.root, Target: string(options.target), Name: options.metadata.ProjectName,
		GoModule: options.metadata.GoModule, FrameworkModule: options.metadata.FrameworkModule, DistSHA256: digest,
	})
	if err != nil {
		return fmt.Errorf("%w: package desktop Web UI: %w", errBuildArtifact, err)
	}
	fmt.Fprintf(stdout, "Artifact: %s\nDist SHA-256: %s\nPackage SHA-256: %s\nManifest: %s\n", artifact.Path, digest, artifact.SHA256, artifact.Manifest)
	return nil
}
