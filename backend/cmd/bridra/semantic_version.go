package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var strictSemanticVersionPattern = regexp.MustCompile(
	`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	matches := strictSemanticVersionPattern.FindStringSubmatch(value)
	if matches == nil {
		return semanticVersion{}, fmt.Errorf("invalid semantic version %q", value)
	}
	parts := make([]int, 3)
	for index, component := range matches[1:4] {
		if len(component) > 1 && component[0] == '0' {
			return semanticVersion{}, fmt.Errorf("numeric component %q has a leading zero", component)
		}
		parsed, err := strconv.Atoi(component)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("parse numeric component %q: %w", component, err)
		}
		parts[index] = parsed
	}
	var prerelease []string
	if matches[4] != "" {
		prerelease = strings.Split(matches[4], ".")
		for _, identifier := range prerelease {
			if numericIdentifier(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, fmt.Errorf(
					"numeric prerelease identifier %q has a leading zero",
					identifier,
				)
			}
		}
	}
	return semanticVersion{
		major: parts[0], minor: parts[1], patch: parts[2], prerelease: prerelease,
	}, nil
}

func compareSemanticVersions(left, right semanticVersion) int {
	for _, pair := range [][2]int{
		{left.major, right.major},
		{left.minor, right.minor},
		{left.patch, right.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		leftIdentifier := left.prerelease[index]
		rightIdentifier := right.prerelease[index]
		if leftIdentifier == rightIdentifier {
			continue
		}
		leftNumeric := numericIdentifier(leftIdentifier)
		rightNumeric := numericIdentifier(rightIdentifier)
		if leftNumeric && rightNumeric {
			if len(leftIdentifier) < len(rightIdentifier) ||
				(len(leftIdentifier) == len(rightIdentifier) && leftIdentifier < rightIdentifier) {
				return -1
			}
			return 1
		}
		if leftNumeric {
			return -1
		}
		if rightNumeric {
			return 1
		}
		if leftIdentifier < rightIdentifier {
			return -1
		}
		return 1
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func numericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
