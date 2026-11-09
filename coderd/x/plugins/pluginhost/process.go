package pluginhost

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
)

// ProcessOptions configures StartProcess.
type ProcessOptions struct {
	// Executable is the binary to run. Defaults to os.Executable().
	Executable string
	// Args are the arguments after the executable. Defaults to
	// []string{"plugin-host"}.
	Args []string
	// Stderr receives the child's stderr, including module output. Nil
	// discards it.
	Stderr io.Writer
}

// processEnv is the complete environment of the plugin host. It must never be
// nil: exec.Cmd treats a nil Env as "inherit", which would hand the plugin
// host coderd's secrets such as CODER_PG_CONNECTION_URL.
func processEnv() []string {
	env := []string{"GOMAXPROCS=1"}
	if runtime.GOOS == "windows" {
		// Windows processes fail to load system DLLs without SystemRoot.
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return env
}

// StartProcess starts a plugin host child process that speaks dRPC on its
// stdin and stdout. The child is killed when ctx is canceled or the client
// is closed.
func StartProcess(ctx context.Context, logger slog.Logger, opts ProcessOptions) (*Client, error) {
	exe := opts.Executable
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return nil, xerrors.Errorf("find executable: %w", err)
		}
	}
	args := opts.Args
	if args == nil {
		args = []string{"plugin-host"}
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, xerrors.Errorf("create stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, xerrors.Errorf("create stdout pipe: %w", err)
	}

	// #nosec G204 -- the executable and arguments come from coderd, not users.
	cmd := exec.Command(exe, args...)
	cmd.Env = processEnv()
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = opts.Stderr
	setProcAttr(cmd)
	err = cmd.Start()
	_ = stdinR.Close()
	_ = stdoutW.Close()
	if err != nil {
		_ = stdinW.Close()
		_ = stdoutR.Close()
		return nil, xerrors.Errorf("start plugin host: %w", err)
	}
	logger = logger.With(slog.F("pid", cmd.Process.Pid))
	kill := func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			logger.Warn(context.Background(), "failed to kill plugin host", slog.Error(err))
		}
	}

	c := newClient(logger, &pipeConn{Reader: stdoutR, Writer: stdinW}, kill)
	go func() {
		err := cmd.Wait()
		_ = stdoutR.Close()
		_ = stdinW.Close()
		logger.Debug(context.Background(), "plugin host exited", slog.Error(err))
		c.exited(err)
	}()
	go func() {
		select {
		case <-ctx.Done():
			kill()
		case <-c.done:
		}
	}()
	return c, nil
}

type pipeConn struct {
	io.Reader
	io.Writer
}

func (p *pipeConn) Close() error {
	var err error
	if c, ok := p.Reader.(io.Closer); ok {
		err = c.Close()
	}
	if c, ok := p.Writer.(io.Closer); ok {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	return err
}
