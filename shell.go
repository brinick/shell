package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ------------------------------------------------------------------

// Run executes the command and returns a Result object.
// The command can be configured via one or more Option functions.
func Run(cmd string, options ...Option) *Result {
	exe := "bash"
	args := append([]string{"-c"}, fmt.Sprintf("%s", cmd))

	shellcmd := newCommand(exe, args, options...)
	shellcmd.run()
	return shellcmd.Result
}

// -------------------------------------------------------------

// Result is the wrapper
type Result struct {
	Stdout   resultOutput
	Stderr   resultOutput
	done     chan struct{}
	Error    resultError
	ExitCode int
	Duration int64

	// Something panicked - process died
	Crashed bool

	// Context timed out - process killed
	TimedOut bool

	// Context cancelled - process killed
	Cancelled bool
}

func (r *Result) String() string {
	return strings.Join(
		[]string{
			fmt.Sprintf("Done? %t", r.IsReady()),
			fmt.Sprintf("Crashed?: %t", r.Crashed),
			fmt.Sprintf("TimedOut?: %t", r.TimedOut),
			fmt.Sprintf("Cancelled?: %t", r.Cancelled),
			fmt.Sprintf("Error?: %t", r.IsError()),
			fmt.Sprintf("ErrMsg: %s", r.Error),
		},
		" - ",
	)
}

// IsError indicates if any error occured in preparing or executing
// the shell command
func (r *Result) IsError() bool {
	return r.Error != nil && len(r.Error) > 0
}

// AddError appends the given error to the ResultError list
func (r *Result) AddError(e error) {
	r.Error.Append(e)
}

func (r *Result) IsReady() bool {
	select {
	case <-r.Ready():
		return true
	default:
		return false
	}
}

// Ready can be used to indicate if the given Result is available
func (r *Result) Ready() <-chan struct{} {
	return r.done
}

// SetReady will declare this Result ready
func (r *Result) SetReady() {
	// allows for the possibility of multiple closure attempts
	select {
	case <-r.done:
		// already closed
	default:
		close(r.done)
	}
}

// ------------------------------------------------------------------

// resultError is a type that aggregates shell command errors
type resultError []error

func (re resultError) Error() string {
	s := []string{}
	for _, e := range re {
		s = append(s, e.Error())
	}

	return strings.Join(s, "\n")
}

// Append will add the given error to the list of errors
func (re *resultError) Append(e error) {
	*re = append(*re, e)
}

func (re resultError) String() string {
	return re.Error()
}

// -------------------------------------------------------------

// resultOutput wraps a bytes buffer
type resultOutput struct {
	b    *bytes.Buffer
	step int
}

func (r *resultOutput) Next() []byte {
	return r.b.Next(r.step)
}

// Text returns the output as a string, stripped of pre/suf-fixed
// whitespace and, optionally, of any trailing return chars
func (r *resultOutput) Text(stripNewLine bool) string {
	val := strings.TrimSpace(string(r.b.Bytes()))
	if !stripNewLine {
		return val
	}
	return strings.TrimSuffix(val, "\n")
}

// Lines returns the output as a slice of strings
func (r *resultOutput) Lines() []string {
	return strings.Split(r.Text(false), "\n")
}

// -------------------------------------------------------------

// command creates a new shell command
func newCommand(executable string, args []string, options ...Option) *command {
	s := &command{
		exe:  executable,
		args: args,
		c:    exec.Command(executable, args...),
		ctx:  context.TODO(),
		Result: &Result{
			Stdout: resultOutput{&bytes.Buffer{}, 2048},
			Stderr: resultOutput{&bytes.Buffer{}, 2048},
			done:   make(chan struct{}, 3), // to ensure we don't block
		},
	}

	for _, option := range options {
		option(s)
	}

	var env = s.env
	if len(env) == 0 {
		// TODO: is this necessary? Presumably by default
		// commands inherit the parent environment if not set explicitly
		env = os.Environ()
	}

	s.c.Env = env

	s.c.Stdout = s.Result.Stdout.b
	s.c.Stderr = s.Result.Stderr.b

	return s
}

// ------------------------------------------------------------------

// command represents a given shell command
type command struct {
	exe     string        // the name or path to the executable
	args    []string      // args passed to the executable
	env     []string      // specific vars that were set/unset
	c       *exec.Cmd     // the wrapped command object
	Result  *Result       // the result object
	timeout time.Duration // 0 = no timeout
	ctx     context.Context
	stop    <-chan struct{} // tell the command to stop
	bkgd    bool            // run in background or not
}

// ------------------------------------------------------------------

func (sc *command) String() string {
	return fmt.Sprintf("%s %s", sc.exe, strings.Join(sc.args, " "))
}

// ------------------------------------------------------------------

func (sc *command) start() error {
	if err := sc.c.Start(); err != nil {
		sc.Result.AddError(err)
		return err
	}

	return nil
}

// ------------------------------------------------------------------

/*
func (sc *command) exec() error {
	if err := sc.c.Wait(); err != nil {
		sc.Result.AddError(err)
		return err
	}

	return nil
}
*/

// ------------------------------------------------------------------

// run will launch the given shell command, returning once the
// command is done, or immediately if the Bkgd Option was set.
func (sc *command) run() {
	if sc.timeout > 0 {
		// augment the context
		var cancelTimeout context.CancelFunc
		sc.ctx, cancelTimeout = context.WithTimeout(sc.ctx, sc.timeout)
		defer cancelTimeout()
	}

	go sc.launch()

	if sc.bkgd {
		go sc.wait()
		return
	}

	sc.wait()
}

func (sc *command) launch() {
	defer func() {
		if r := recover(); r != nil {
			sc.Result.Crashed = true
			sc.Result.AddError(fmt.Errorf("Crashed with: %s", r))
		}
		sc.Result.SetReady()
	}()

	started := time.Now().Unix()
	if err := sc.c.Run(); err != nil {
		sc.Result.AddError(err)
	}

	fmt.Println("STDOUT EXISTS?", sc.c.Stdout == nil)
	sc.Result.Duration = time.Now().Unix() - started
}

// ------------------------------------------------------------------

func (sc *command) wait() {
	defer sc.Result.SetReady()

	select {
	case <-sc.stop:
		sc.Result.Cancelled = true
		sc.Result.AddError(fmt.Errorf("Cancelled"))
		sc.kill()
	case <-sc.Result.Ready():
		// all ok, process is done
	case <-sc.ctx.Done():
		err := sc.ctx.Err()
		switch err {
		case context.DeadlineExceeded:
			sc.Result.TimedOut = true
		case context.Canceled:
			sc.Result.Cancelled = true
		}
		sc.Result.AddError(err)
		sc.kill()
	}
}

// ------------------------------------------------------------------

// Kill will terminate the internal exec.Cmd.Process
func (sc *command) kill() error {
	if err := sc.c.Process.Kill(); err != nil {
		sc.Result.AddError(err)
		return err
	}

	return nil
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

// Bkgd is an Option to run the command in the background
// and return a Result object immediately that will be
// filled in later. Using this one can access the Result
// Stdout/Stderr streams in real-time. When this option
// is not set, the Run function will block until the command
// is done, only then returning a Result object. This is
// the default.
func Bkgd() Option {
	return func(s *command) {
		s.bkgd = true
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

// ------------------------------------------------------------------
