package webdesktop

import "testing"

func TestLauncherNameCanCoexistWithDist(t *testing.T) {
	for _, tc := range []struct{ target, name, want string }{
		{"linux", "dist", "dist-app"}, {"linux", "portal", "portal"},
		{"macos", "dist", "dist"}, {"windows", "dist", "dist.exe"},
	} {
		if got := launcherFilename(tc.target, tc.name); got != tc.want {
			t.Errorf("%s/%s: got %q", tc.target, tc.name, got)
		}
	}
}
