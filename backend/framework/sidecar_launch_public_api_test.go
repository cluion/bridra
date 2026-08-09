package framework_test

import (
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestSidecarLaunchPublicAPI(t *testing.T) {
	launch, err := framework.ReadSidecarLaunch(
		[]string{"--token-stdin"},
		strings.NewReader(`{"protocolVersion":1,"token":"token"}`+"\n"),
	)
	if err != nil {
		t.Fatalf("ReadSidecarLaunch returned error: %v", err)
	}
	if launch.Token != "token" || !launch.UsesStdinHandshake {
		t.Fatalf("unexpected launch: %#v", launch)
	}
	if framework.SidecarLaunchProtocolVersion != 1 {
		t.Fatalf("unexpected launch protocol: %d", framework.SidecarLaunchProtocolVersion)
	}
	if framework.SidecarLaunchReadyMessage == "" {
		t.Fatal("SidecarLaunchReadyMessage is empty")
	}
}
