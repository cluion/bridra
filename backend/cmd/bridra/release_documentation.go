package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

var releaseDocumentationVersionPattern = regexp.MustCompile(
	strings.TrimSuffix(strings.TrimPrefix(strictSemanticVersionPattern.String(), "^"), "$"),
)

const (
	releaseDocumentationStart = "<!-- bridra:release-current:start -->"
	releaseDocumentationEnd   = "<!-- bridra:release-current:end -->"
)

// Only explicitly maintained examples follow the release version. Historical
// transitions and compatibility notes outside these blocks remain immutable.
func updateReleaseDocumentation(contents []byte, current, target string) ([]byte, error) {
	startMarker, endMarker := []byte(releaseDocumentationStart), []byte(releaseDocumentationEnd)
	var result bytes.Buffer
	remaining := contents
	blocks := 0
	for {
		start, end := bytes.Index(remaining, startMarker), bytes.Index(remaining, endMarker)
		if start < 0 {
			if end >= 0 {
				return nil, fmt.Errorf("release-current end marker has no start")
			}
			result.Write(remaining)
			break
		}
		if end < 0 || end < start {
			return nil, fmt.Errorf("release-current markers are incomplete or out of order")
		}
		bodyStart := start + len(startMarker)
		body := remaining[bodyStart:end]
		if bytes.Contains(body, startMarker) {
			return nil, fmt.Errorf("release-current blocks must not nest")
		}
		foundVersion := false
		updatedBody := releaseDocumentationVersionPattern.ReplaceAllFunc(body, func(version []byte) []byte {
			if bytes.Equal(version, []byte(current)) {
				foundVersion = true
				return []byte(target)
			}
			return version
		})
		if !foundVersion {
			return nil, fmt.Errorf("release-current block %d does not reference %s", blocks+1, current)
		}
		result.Write(remaining[:bodyStart])
		result.Write(updatedBody)
		result.Write(endMarker)
		remaining = remaining[end+len(endMarker):]
		blocks++
	}
	if blocks == 0 {
		return nil, fmt.Errorf("release-current documentation block not found")
	}
	return result.Bytes(), nil
}
