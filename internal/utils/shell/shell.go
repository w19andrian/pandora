package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"pandora/internal/utils/constants"
)

type CmdSpec struct {
	Name string
	Args []string
}

type CmdResult struct {
	ExitCode  int
	Output    string
	StartedAt time.Time
	Duration  time.Duration
}

func (spec CmdSpec) NewCmd(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, spec.Name, spec.Args...)
}

// RunOptions defines the behaviour of the execution
//
//	Interactive:		Whether stdin should be forwarded or not
//	Stream:			Streams output into the foreground
type RunOptions struct {
	Interactive bool

	StreamOutput  bool
	CaptureOutput bool

	Stdout io.Writer
	Stderr io.Writer
}

func DefaultRunOptions() RunOptions {
	return RunOptions{
		Interactive:   false,
		StreamOutput:  false,
		CaptureOutput: true,
		Stdout:        nil,
		Stderr:        nil,
	}
}

func (o RunOptions) validate() error {
	if !o.StreamOutput && !o.CaptureOutput {
		return errors.New("invalid options: at least one of StreamOutput or CaptureOutput must be true")
	}
	return nil
}

func (o RunOptions) resolvedStdout() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return os.Stdout
}

func (o RunOptions) resolvedStderr() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return os.Stderr
}

// Run with RunOptions.Interactive false runs without forwarding stdin.
// It captures a combined stdout and stderr
// Non-zero exit code from successful execution is NOT returned as error.
// It is stored in CmdResult.ExitCode.
func (spec CmdSpec) Run(ctx context.Context, opts RunOptions) (CmdResult, error) {
	if err := opts.validate(); err != nil {
		return CmdResult{}, err
	}

	start := time.Now()
	cmd := spec.NewCmd(ctx)

	cmd.Stdin = os.Stdin

	if !opts.Interactive {
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			return CmdResult{}, fmt.Errorf("open %s: %w", os.DevNull, err)
		}
		defer devNull.Close()
		cmd.Stdin = devNull
	}

	var buf bytes.Buffer
	var captureWriter io.Writer
	if opts.CaptureOutput {
		captureWriter = &buf
	}

	switch {
	case opts.StreamOutput && opts.CaptureOutput:
		// Tee like: stream + capture
		cmd.Stdout = io.MultiWriter(opts.resolvedStdout(), captureWriter)
		cmd.Stderr = io.MultiWriter(opts.resolvedStderr(), captureWriter)

	case opts.StreamOutput && !opts.CaptureOutput:
		// Only stream output
		cmd.Stdout = opts.resolvedStdout()
		cmd.Stderr = opts.resolvedStderr()

	case !opts.StreamOutput && opts.CaptureOutput:
		// Only capture output
		cmd.Stdout = captureWriter
		cmd.Stderr = captureWriter
	}

	err := cmd.Run()
	duration := time.Since(start)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return CmdResult{
			ExitCode:  exitCodeFromErr(ctxErr),
			Output:    buf.String(),
			StartedAt: start,
			Duration:  duration,
		}, ctxErr
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return CmdResult{
				ExitCode:  exitErr.ExitCode(),
				Output:    buf.String(),
				StartedAt: start,
				Duration:  duration,
			}, nil
		}
		return CmdResult{}, err
	}

	return CmdResult{
		ExitCode:  0,
		Output:    buf.String(),
		StartedAt: start,
		Duration:  duration,
	}, nil
}

func exitCodeFromErr(err error) int {
	if errors.Is(err, context.Canceled) {
		return constants.ErrCtxCanceled
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return constants.ErrCtxDeadlineExceeded
	}

	return constants.ErrDefault
}
