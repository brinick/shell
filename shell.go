package shell

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-cmd/cmd"
)

// ------------------------------------------------------------------

// Run executes the command and returns a Result object.
// The command can be configured via one or more Option functions.
func Run(command string, options ...Option) *Result {
	exe := "/bin/bash"
	args := append([]string{"-c"}, fmt.Sprintf("%s", command))

	shellcmd := newCommand(exe, args, options...)
	shellcmd.run()
	return shellcmd.Result
}

// -------------------------------------------------------------

// Result is the wrapper
type Result struct {
	// Something panicked - process died
	crashed     bool
	crashReason string

	// Context timed out - process killed
	timedOut bool

	// Context canceled - process killed
	canceled bool

	current func() cmd.Status
	final   *cmd.Status
	done    func() <-chan struct{}

	nStdout int
	nStderr int
}

// IsReady returns a bool indicating if the command
// is done, and the Result object useable
func (r *Result) IsReady() bool {
	if r == nil {
		return false
	}
	select {
	case <-r.done():
		return true
	default:
		return false
	}
}

// Ready returns a channel to indicate if
// the command is done
func (r *Result) Ready() <-chan struct{} {
	return r.done()
}

func (r *Result) status() *cmd.Status {
	if r.final != nil {
		return r.final
	}

	curr := r.current()
	return &curr
}

// ExitCode returns the exit code of the process
func (r *Result) ExitCode() int {
	return r.status().Exit
}

// PID returns the process PID of the command
func (r *Result) PID() int {
	return r.status().PID
}

// Duration indicates for how long the command ran
func (r *Result) Duration() float64 {
	return r.status().Runtime
}

// Stdout returns an Output object wrapping the latest lines
// from the stdout stream
func (r *Result) Stdout() *Output {
	lines := r.status().Stdout[r.nStdout:]
	r.nStdout += len(lines)
	return &Output{lines}
}

// Stderr returns an Output object wrapping the latest lines
// from the stderr stream
func (r *Result) Stderr() *Output {
	lines := r.status().Stderr[r.nStderr:]
	r.nStderr += len(lines)
	return &Output{lines}
}

// IsError indicates if any error occured in preparing or executing
// the shell command. This will return false if the command ran ok,
// but just had a non-zero exit code.
func (r *Result) IsError() bool {
	return r.Err() != nil
}

// Err returns an eventual error from running the command
func (r *Result) Err() error {
	return r.status().Error
}

// Crashed indicates if the command crashed
func (r *Result) Crashed() bool {
	return r.crashed
}

// Canceled indicates if the command Context was canceled
func (r *Result) Canceled() bool {
	return r.canceled
}

// TimedOut indicates if the command Context timed out
func (r *Result) TimedOut() bool {
	return r.timedOut
}

// ------------------------------------------------------------------

// Output is a structure to wrap the shell command output stream
type Output struct {
	lines []string
}

// Empty checks if there is any output
func (o *Output) Empty() bool {
	return len(o.lines) == 0
}

// Lines returns the output as newline split list of lines
func (o *Output) Lines() []string {
	return o.lines
}

// Text returns the output as a single string
func (o *Output) Text() string {
	return strings.Join(o.lines, "\n")
}

// ------------------------------------------------------------------

// command represents a given shell command
type command struct {
	c      *cmd.Cmd // the wrapped command object
	Result *Result  // the result object

	// options
	env     []string
	ctx     context.Context
	stop    <-chan struct{}
	bkgd    bool
	timeout time.Duration // 0 = no timeout
}

// ------------------------------------------------------------------

// newCommand creates a new shell command
func newCommand(executable string, args []string, options ...Option) *command {
	s := &command{
		ctx:    context.TODO(),
		c:      cmd.NewCmd(executable, args...),
		Result: &Result{},
	}

	for _, option := range options {
		option(s)
	}

	s.c.Env = s.env
	s.Result.done = s.c.Done
	s.Result.current = s.c.Status

	return s
}

// ------------------------------------------------------------------

// run will launch the given shell command, returning once the command is done
func (sc *command) run() {
	defer func() {
		if r := recover(); r != nil {
			sc.Result.crashed = true
			sc.Result.crashReason = r.(string)
		}
	}()

	var (
		timeoutAdded = make(chan struct{})
		statusChan   <-chan cmd.Status
	)

	go func() {
		if sc.timeout > 0 {
			// augment the context
			var cancelTimeout context.CancelFunc
			sc.ctx, cancelTimeout = context.WithTimeout(sc.ctx, sc.timeout)
			defer cancelTimeout()
		}
		close(timeoutAdded)

		// exit only when the command is done, so as not to
		// prematurely invoke the deferred timeout cancel
		<-sc.Result.Ready()
	}()

	// block until the other routine has augmented
	// any timeout to the shell command instance
	<-timeoutAdded

	statusChan = sc.c.Start()
	if sc.bkgd {
		go sc.wait(statusChan)
		return
	}

	sc.wait(statusChan)
}

// ------------------------------------------------------------------

func (sc *command) wait(statusChan <-chan cmd.Status) {
	select {
	case final := <-statusChan:
		// process is done; grab the final full output
		sc.Result.final = &final
	case <-sc.stop:
		sc.Result.canceled = true
		sc.kill()
	case <-sc.ctx.Done():
		err := sc.ctx.Err()
		switch err {
		case context.DeadlineExceeded:
			sc.Result.timedOut = true
		case context.Canceled:
			sc.Result.canceled = true
		}
		sc.kill()
	}
}

// ------------------------------------------------------------------

// Kill will terminate the internal cmd.Cmd
func (sc *command) kill() {
	sc.c.Stop()
}

// ------------------------------------------------------------------
// ------------------------------------------------------------------
// ------------------------------------------------------------------

// Option is a function that modifies the given shell command
type Option func(s *command)

// Context is an Option to set a context on the command
// that will interrupt the command if the context is done.
// Only the last call to this function will be taken into
// account.
func Context(ctx context.Context) Option {
	return func(s *command) {
		s.ctx = ctx
	}
}

// Timeout is an Option to provide a timed shell command.
// Only the last call to this funcion will be taken into account.
// By default, commands do not timeout.
func Timeout(d time.Duration) Option {
	return func(s *command) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// Env is an Option to modify the shell command's
// execution environment. Multiple calls to this function
// will be taken into account, with all values passed
// appended into a single slice.
func Env(values []string) Option {
	return func(s *command) {
		if len(s.env) > 0 {
			s.env = append(s.env, values...)
		} else {
			s.env = append(os.Environ(), values...)
		}
	}
}

// Cancel is an Option to provide a channel whose writing to,
// or closing, will indicate that the command should stop
func Cancel(stop <-chan struct{}) func(*command) {
	return func(s *command) {
		s.stop = stop
	}
}

// Bkgd is an Option to make the command run in the background
func Bkgd() func(*command) {
	return func(s *command) {
		s.bkgd = true
	}
}

// ------------------------------------------------------------------
