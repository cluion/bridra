package framework

import (
	"io"
	"strings"
	"testing"
)

func TestReadSidecarLaunchFromStdinPreservesBufferedRPCInput(t *testing.T) {
	input := strings.NewReader(
		`{"protocolVersion":1,"token":"secret-token"}` + "\n" +
			`{"id":"request-1"}` + "\n",
	)

	launch, err := ReadSidecarLaunch([]string{"--token-stdin"}, input)
	if err != nil {
		t.Fatalf("ReadSidecarLaunch returned error: %v", err)
	}
	if launch.Token != "secret-token" {
		t.Fatalf("Token = %q, want secret-token", launch.Token)
	}
	if !launch.UsesStdinHandshake {
		t.Fatal("UsesStdinHandshake = false, want true")
	}
	remaining, err := io.ReadAll(launch.Input)
	if err != nil {
		t.Fatalf("read remaining input: %v", err)
	}
	if got, want := string(remaining), `{"id":"request-1"}`+"\n"; got != want {
		t.Fatalf("remaining input = %q, want %q", got, want)
	}
}

func TestReadSidecarLaunchSupportsLegacyTokenArgument(t *testing.T) {
	input := strings.NewReader("rpc input")
	launch, err := ReadSidecarLaunch([]string{"--token", "legacy-token"}, input)
	if err != nil {
		t.Fatalf("ReadSidecarLaunch returned error: %v", err)
	}
	if launch.Token != "legacy-token" {
		t.Fatalf("Token = %q, want legacy-token", launch.Token)
	}
	if launch.Input != input {
		t.Fatal("legacy launch replaced the input reader")
	}
	if launch.UsesStdinHandshake {
		t.Fatal("UsesStdinHandshake = true, want false")
	}
}

func TestReadSidecarLaunchRejectsInvalidArgumentsWithoutLeakingToken(t *testing.T) {
	secret := "do-not-leak-this-token"
	tests := [][]string{
		nil,
		{"--token", secret, "extra"},
		{"--token", secret, "--token-stdin"},
		{"--unknown", secret},
	}
	for _, args := range tests {
		_, err := ReadSidecarLaunch(args, strings.NewReader(""))
		if err == nil {
			t.Fatalf("ReadSidecarLaunch(%v) returned no error", args)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked token: %v", err)
		}
	}
}

func TestReadSidecarLaunchRejectsInvalidHandshakesWithoutLeakingToken(t *testing.T) {
	secret := "do-not-leak-this-token"
	tests := []string{
		"",
		`{"protocolVersion":1,"token":"` + secret + `"}`,
		`{"protocolVersion":2,"token":"` + secret + `"}` + "\n",
		`{"protocolVersion":1,"token":""}` + "\n",
		`{"protocolVersion":1,"token":"` + secret + `","extra":true}` + "\n",
		`{"protocolVersion":1,"token":"` + secret + `"} {}` + "\n",
		strings.Repeat("x", maxSidecarLaunchFrameBytes+1) + "\n",
	}
	for _, input := range tests {
		_, err := ReadSidecarLaunch(
			[]string{"--token-stdin"},
			strings.NewReader(input),
		)
		if err == nil {
			t.Fatalf("ReadSidecarLaunch accepted invalid handshake of size %d", len(input))
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked token: %v", err)
		}
	}
}

func TestReadSidecarLaunchRejectsOversizedToken(t *testing.T) {
	token := strings.Repeat("t", maxSidecarLaunchTokenBytes+1)
	_, err := ReadSidecarLaunch(
		[]string{"--token-stdin"},
		strings.NewReader(
			`{"protocolVersion":1,"token":"`+token+`"}`+"\n",
		),
	)
	if err == nil {
		t.Fatal("ReadSidecarLaunch accepted oversized token")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked token: %v", err)
	}
}
