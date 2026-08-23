package projectplatform

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolvePresetsAndCanonicalCustomSelection(t *testing.T) {
	tests := []struct {
		selection string
		want      []string
	}{
		{selection: "", want: All},
		{selection: "all", want: All},
		{selection: "desktop", want: []string{Linux, MacOS, Windows}},
		{selection: "mobile", want: []string{Android, IOS}},
		{selection: "web,macos", want: []string{MacOS, Web}},
	}
	for _, test := range tests {
		t.Run(test.selection, func(t *testing.T) {
			got, err := Resolve(test.selection)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("platforms = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveRejectsUnknownEmptyAndDuplicateCustomPlatforms(t *testing.T) {
	for _, selection := range []string{"linux,linux", "linux,", "visionos"} {
		t.Run(selection, func(t *testing.T) {
			_, err := Resolve(selection)
			if err == nil || (!strings.Contains(err.Error(), "platform") &&
				!strings.Contains(err.Error(), "duplicate")) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
