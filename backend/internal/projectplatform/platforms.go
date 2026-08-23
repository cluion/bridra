package projectplatform

import (
	"fmt"
	"strings"
)

const (
	Android = "android"
	IOS     = "ios"
	Linux   = "linux"
	MacOS   = "macos"
	Windows = "windows"
	Web     = "web"
)

var All = []string{Android, IOS, Linux, MacOS, Windows, Web}

func Resolve(selection string) ([]string, error) {
	selection = strings.TrimSpace(strings.ToLower(selection))
	switch selection {
	case "", "all":
		return CloneAll(), nil
	case "desktop":
		return []string{Linux, MacOS, Windows}, nil
	case "mobile":
		return []string{Android, IOS}, nil
	default:
		return Normalize(strings.Split(selection, ","))
	}
}

func Normalize(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("platform selection must not be empty")
	}
	selected := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if !Contains(All, value) {
			return nil, fmt.Errorf(
				"unsupported platform %q; expected android, ios, linux, macos, windows, or web",
				value,
			)
		}
		if _, exists := selected[value]; exists {
			return nil, fmt.Errorf("duplicate platform %q", value)
		}
		selected[value] = struct{}{}
	}
	result := make([]string, 0, len(selected))
	for _, platform := range All {
		if _, exists := selected[platform]; exists {
			result = append(result, platform)
		}
	}
	return result, nil
}

func Contains(platforms []string, target string) bool {
	for _, platform := range platforms {
		if platform == target {
			return true
		}
	}
	return false
}

func CloneAll() []string {
	return append([]string(nil), All...)
}

func Summary(platforms []string) string {
	if len(platforms) == len(All) {
		all := true
		for index := range All {
			all = all && platforms[index] == All[index]
		}
		if all {
			return "six platforms"
		}
	}
	if len(platforms) == 1 {
		return platforms[0]
	}
	return fmt.Sprintf("%d selected platforms", len(platforms))
}
