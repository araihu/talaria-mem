package codex

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
)

type ErrorClass = curation.ErrorClass

const (
	ErrorUnavailable    = curation.ErrorUnavailable
	ErrorTimeout        = curation.ErrorTimeout
	ErrorRateLimit      = curation.ErrorRateLimit
	ErrorAuthentication = curation.ErrorAuthentication
)

func IsClass(err error, class ErrorClass) bool {
	var providerErr *curation.ProviderError
	return errors.As(err, &providerErr) && providerErr.Class == class
}

type ProcessConfig struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
	Timeout time.Duration
	Handler jsonrpc2.Handler
}

type Process struct {
	command *exec.Cmd
	conn    *jsonrpc2.Conn
	done    chan struct{}
	closed  chan struct{}
	cancel  context.CancelFunc

	mu  sync.RWMutex
	err error

	closeOnce sync.Once
}

func StartProcess(parent context.Context, configuration ProcessConfig) (*Process, error) {
	if configuration.Command == "" {
		return nil, curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex executable is empty"))
	}
	if parent == nil {
		parent = context.Background()
	}
	processContext, cancel := context.WithCancel(parent)
	command := exec.CommandContext(processContext, configuration.Command, configuration.Args...)
	if configuration.Dir != "" {
		command.Dir = configuration.Dir
	}
	if configuration.Env != nil {
		command.Env = append([]string(nil), configuration.Env...)
	} else {
		command.Env = os.Environ()
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, curation.NewProviderError(curation.ErrorUnavailable, err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		_ = stdout.Close()
		return nil, curation.NewProviderError(curation.ErrorUnavailable, err)
	}
	command.Stderr = &limitedWriter{limit: 16 << 10}
	if err := command.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, curation.NewProviderError(curation.ErrorUnavailable, err)
	}
	stream := &processStream{Reader: stdout, Writer: stdin, closers: []io.Closer{stdin, stdout}}
	handler := configuration.Handler
	if handler == nil {
		handler = rejectRequestsHandler{}
	}
	connection := jsonrpc2.NewConn(processContext, jsonrpc2.NewPlainObjectStream(stream), handler)
	process := &Process{command: command, conn: connection, done: make(chan struct{}), closed: make(chan struct{}), cancel: cancel}
	go process.wait()
	return process, nil
}

func (process *Process) Connection() *jsonrpc2.Conn {
	if process == nil {
		return nil
	}
	return process.conn
}

func (process *Process) Done() <-chan struct{} {
	if process == nil {
		return closedChannel()
	}
	return process.done
}

func (process *Process) Err() error {
	if process == nil {
		return curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex process is nil"))
	}
	process.mu.RLock()
	defer process.mu.RUnlock()
	return process.err
}

func (process *Process) wait() {
	err := process.command.Wait()
	process.mu.Lock()
	if err != nil {
		process.err = curation.NewProviderError(curation.ErrorUnavailable, err)
	} else if process.cancelledByClose() {
		process.err = nil
	} else {
		process.err = curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex app-server exited"))
	}
	process.mu.Unlock()
	close(process.done)
}

func (process *Process) cancelledByClose() bool {
	select {
	case <-process.closed:
		return true
	default:
		return false
	}
}

func (process *Process) Close() error {
	if process == nil {
		return nil
	}
	process.closeOnce.Do(func() {
		close(process.closed)
		process.cancel()
		_ = process.conn.Close()
	})
	select {
	case <-process.done:
		return nil
	case <-time.After(2 * time.Second):
		if process.command.Process != nil {
			_ = process.command.Process.Kill()
		}
		<-process.done
		return nil
	}
}

type processStream struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (stream *processStream) Close() error {
	var first error
	for _, closer := range stream.closers {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

type limitedWriter struct {
	limit int
	data  []byte
}

func (writer *limitedWriter) Write(data []byte) (int, error) {
	originalLength := len(data)
	if writer.limit > len(writer.data) {
		remaining := writer.limit - len(writer.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		writer.data = append(writer.data, data...)
	}
	return originalLength, nil
}

type rejectRequestsHandler struct{}

func (rejectRequestsHandler) Handle(ctx context.Context, connection *jsonrpc2.Conn, request *jsonrpc2.Request) {
	if request == nil || request.Notif {
		return
	}
	_ = connection.ReplyWithError(ctx, request.ID, &jsonrpc2.Error{Code: -32601, Message: "requests disabled"})
}

func closedChannel() <-chan struct{} {
	channel := make(chan struct{})
	close(channel)
	return channel
}
