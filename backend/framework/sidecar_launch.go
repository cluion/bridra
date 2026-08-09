package framework

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
)

const (
	// SidecarLaunchProtocolVersion identifies the stdin launch handshake format.
	SidecarLaunchProtocolVersion = 1

	// SidecarLaunchReadyMessage is emitted on stderr after a valid stdin launch
	// handshake has been consumed. It is intentionally free of credentials.
	SidecarLaunchReadyMessage = "sidecar: launch ready 1"

	maxSidecarLaunchFrameBytes = 4096
	maxSidecarLaunchTokenBytes = 1024
)

// SidecarLaunch contains the authenticated launch settings and the remaining
// stdin stream. Input must be used by the RPC server so bytes buffered while
// reading the launch handshake are preserved.
type SidecarLaunch struct {
	Token              string
	Input              io.Reader
	UsesStdinHandshake bool
}

type sidecarLaunchHandshake struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Token           string `json:"token"`
}

// ReadSidecarLaunch reads either the current stdin launch handshake or the
// legacy --token argument. New callers should use --token-stdin so the token is
// not exposed in process arguments.
func ReadSidecarLaunch(args []string, input io.Reader) (SidecarLaunch, error) {
	flags := flag.NewFlagSet("sidecar", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	token := flags.String("token", "", "legacy launch token")
	tokenStdin := flags.Bool("token-stdin", false, "read launch token from stdin")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return SidecarLaunch{}, errors.New("invalid sidecar launch arguments")
	}
	if *tokenStdin == (*token != "") {
		return SidecarLaunch{}, errors.New("exactly one sidecar launch token source is required")
	}

	if !*tokenStdin {
		if len(*token) > maxSidecarLaunchTokenBytes {
			return SidecarLaunch{}, errors.New("sidecar launch token is too large")
		}
		return SidecarLaunch{Token: *token, Input: input}, nil
	}
	if input == nil {
		return SidecarLaunch{}, errors.New("sidecar launch input is required")
	}

	buffered := bufio.NewReaderSize(input, maxSidecarLaunchFrameBytes)
	line, err := buffered.ReadSlice('\n')
	if err != nil {
		return SidecarLaunch{}, errors.New("invalid sidecar launch handshake")
	}
	if len(line) > maxSidecarLaunchFrameBytes {
		return SidecarLaunch{}, errors.New("sidecar launch handshake is too large")
	}

	var handshake sidecarLaunchHandshake
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&handshake); err != nil {
		return SidecarLaunch{}, errors.New("invalid sidecar launch handshake")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return SidecarLaunch{}, errors.New("invalid sidecar launch handshake")
	}
	if handshake.ProtocolVersion != SidecarLaunchProtocolVersion {
		return SidecarLaunch{}, errors.New("unsupported sidecar launch protocol")
	}
	if handshake.Token == "" {
		return SidecarLaunch{}, errors.New("sidecar launch token is required")
	}
	if len(handshake.Token) > maxSidecarLaunchTokenBytes {
		return SidecarLaunch{}, errors.New("sidecar launch token is too large")
	}

	return SidecarLaunch{
		Token:              handshake.Token,
		Input:              buffered,
		UsesStdinHandshake: true,
	}, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}
