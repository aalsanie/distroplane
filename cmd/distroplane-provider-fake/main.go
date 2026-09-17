package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aalsanie/distroplane/internal/fakeprovider"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const processModeEnv = "DISTROPLANE_FAKE_PROCESS_MODE"
const delayMillisEnv = "DISTROPLANE_FAKE_DELAY_MS"

func main() {
	os.Exit(run(context.Background(), os.Stdin, os.Stdout, os.Stderr, os.Getenv, time.Sleep))
}

func run(ctx context.Context, input io.Reader, output, diagnostics io.Writer, getenv func(string) string, sleep func(time.Duration)) int {
	mode := strings.TrimSpace(getenv(processModeEnv))
	switch mode {
	case "":
		mode = "normal"
	case "empty":
		return 0
	case "malformed":
		_, _ = io.WriteString(output, "{")
		return 0
	case "oversized":
		_, _ = io.WriteString(output, strings.Repeat("x", protocol.DefaultMaxMessageBytes+1))
		return 0
	case "crash":
		_, _ = io.WriteString(diagnostics, "fake provider crash\n")
		return 70
	case "timeout":
		delay := 10000
		if raw := strings.TrimSpace(getenv(delayMillisEnv)); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				_, _ = fmt.Fprintln(diagnostics, "invalid fake delay")
				return 64
			}
			delay = parsed
		}
		sleep(time.Duration(delay) * time.Millisecond)
		mode = "normal"
	case "trailing":
		var buffer bytes.Buffer
		if err := protocol.ServeOnce(ctx, input, &buffer, fakeprovider.Provider{}, protocol.NewCodec(protocol.DefaultMaxMessageBytes)); err != nil {
			_, _ = fmt.Fprintln(diagnostics, err)
			return 1
		}
		_, _ = output.Write(buffer.Bytes())
		_, _ = io.WriteString(output, " trailing")
		return 0
	case "mismatched_request_id":
		codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
		var buffer bytes.Buffer
		if err := protocol.ServeOnce(ctx, input, &buffer, fakeprovider.Provider{}, codec); err != nil {
			_, _ = fmt.Fprintln(diagnostics, err)
			return 1
		}
		response, err := codec.DecodeResponse(&buffer)
		if err != nil {
			_, _ = fmt.Fprintln(diagnostics, err)
			return 1
		}
		response.RequestID = "mismatched-" + response.RequestID
		if err := codec.EncodeResponse(output, response); err != nil {
			_, _ = fmt.Fprintln(diagnostics, err)
			return 1
		}
		return 0
	case "normal":
	default:
		_, _ = fmt.Fprintf(diagnostics, "unknown fake process mode %q\n", mode)
		return 64
	}

	if err := protocol.ServeOnce(ctx, input, output, fakeprovider.Provider{}, protocol.NewCodec(protocol.DefaultMaxMessageBytes)); err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 1
	}
	return 0
}
