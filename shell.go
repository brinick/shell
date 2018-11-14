package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brinick/logging"
)

// ------------------------------------------------------------------

// Run executes and waits for a command to complete,
// returning a Result object. The internal shell command
// object can be configured via one or more shell.Options.
func Run(cmd string, options ...Option) *Result {
	return RunWithContext(context.TODO(), cmd, options...)
}

// RunWithContext execute the command, waiting for it it complete
// before returning a Result object. The command will abort early
// if the context is done.
func RunWithContext(ctx context.Context, cmd string, options ...Option) *Result {
	exe := "bash"
	args := append([]string{"-c"}, fmt.Sprintf("%s", cmd))

	shellcmd := newCommand(exe, args, options...)
	shellcmd.runWithContext(ctx)
	return shellcmd.Result
}

// -------------------------------------------------------------

// Result is the wrapper
type Result struct {
	Stdout    resultOutput // stdout
	Stderr    resultOutput // stderr
	done      chan struct{}
	Error     resultError
	Killed    bool
	Cancelled bool
	TimedOut  bool
	ExitCode  int
	Duration  int64
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

// Ready can be used to indicate if the given Result is available
func (r *Result) Ready() <-chan struct{} {
	return r.done
}

// SetReady will declare this Result ready
func (r *Result) SetReady() {
	r.done <- struct{}{}
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
	b *bytes.Buffer
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

// command represents a given shell command
type command struct {
	exe     string          // the name or path to the executable
	args    []string        // args passed to the executable
	env     []string        // specific vars that were set/unset
	c       *exec.Cmd       // the wrapped command object
	Result  *Result         // the result object
	timeout int             // 0 = no timeout
	stop    <-chan struct{} // tell the command to stop
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

func (sc *command) exec() error {
	if err := sc.c.Wait(); err != nil {
		sc.Result.AddError(err)
		return err
	}

	return nil
}

// ------------------------------------------------------------------

// runWithContext will execute the given shell command returning a Result.
// The caller may stop the command by calling the command.Stop() method.
// Alternatively, a timeout may be provided to place an upper bound.
func (sc *command) runWithContext(ctx context.Context) *Result {
	defer func() {
		sc.Result.SetReady()
	}()

	if sc.timeout > 0 {
		// augment the context
		var cancelTimeout context.CancelFunc
		duration := time.Duration(sc.timeout) * time.Second
		ctx, cancelTimeout = context.WithTimeout(ctx, duration)
		defer cancelTimeout()
	}

	go func() {
		defer sc.Result.SetReady()

		started := time.Now().Unix()
		if err := sc.start(); err != nil {
			sc.Result.AddError(err)
			return
		}
		sc.exec()
		sc.Result.Duration = time.Now().Unix() - started
	}()

	select {
	case <-sc.stop:
		sc.Result.Cancelled = true
		sc.Result.AddError(fmt.Errorf("Cancelled"))
		sc.kill()
	case <-sc.Result.Ready():
		// all ok, process is done
	case <-ctx.Done():
		err := ctx.Err()
		// not sure what context expired, let's check
		switch err {
		case context.DeadlineExceeded:
			sc.Result.TimedOut = true
		case context.Canceled:
			sc.Result.Cancelled = true
		}
		sc.Result.AddError(err)
		sc.kill()
	}

	return sc.Result
}

// ------------------------------------------------------------------

func (sc *command) run() *Result {
	return sc.runWithContext(context.TODO())
}

// ------------------------------------------------------------------

// Kill will terminate the internal exec.Cmd.Process
func (sc *command) kill() error {
	sc.Result.Killed = true

	if err := sc.c.Process.Kill(); err != nil {
		sc.Result.AddError(err)
		return err
	}

	return nil
}

// ------------------------------------------------------------------

// Option is a function that modifies the given shell command
type Option func(s *command)

// Timeout is an Option to provide a timed shell command
func Timeout(secs int) func(*command) {
	return func(s *command) {
		if secs > 0 {
			s.timeout = secs
		}
	}
}

// Env is an Option to modify the shell command's execution environment
func Env(values []string) func(*command) {
	return func(s *command) {
		if len(s.env) > 0 {
			s.env = append(s.env, values...)
		} else {
			s.env = append(os.Environ(), values...)
		}
	}
}

// Cancel provides a channel that can be used to tell the ongoing
// command to stop running
func Cancel(stop <-chan struct{}) func(*command) {
	return func(s *command) {
		s.stop = stop
	}
}

// ------------------------------------------------------------------

// command creates a new shell command
func newCommand(executable string, args []string, options ...Option) *command {
	s := &command{
		exe:  executable,
		args: args,
		c:    exec.Command(executable, args...),
		Result: &Result{
			Stdout: resultOutput{&bytes.Buffer{}},
			Stderr: resultOutput{&bytes.Buffer{}},
			done:   make(chan struct{}, 3), // to ensure we don't block
		},
	}

	for _, option := range options {
		option(s)
	}

	var env = s.env
	if len(env) == 0 {
		env = os.Environ()
	}

	s.c.Env = env
	s.c.Stdout = s.Result.Stdout.b
	s.c.Stderr = s.Result.Stderr.b

	return s
}

// ------------------------------------------------------------------

// DirTreeSize walks the tree starting at root directory,
// and totals the size of all files it finds. Directories
// matching entries in the excludeDirs list are not traversed.
// The grand total in bytes is returned.
func DirTreeSize(root string, excludeDirs []string) (int64, error) {
	totSize := int64(0)
	err := filepath.Walk(
		root,
		func(path string, pathInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if pathInfo.IsDir() {
				for _, e := range excludeDirs {
					if pathInfo.Name() == e {
						return filepath.SkipDir
					}
				}
			} else {
				totSize += pathInfo.Size()
			}

			return nil
		},
	)

	return totSize, err
}

// DirDepth returns the integer number of directories that
// path is below root. If root is not a prefix of path, it
// returns 0. If path is a file, the depth is calculated with
// respect to the parent directory of the file.
func DirDepth(root, path string) (int, error) {
	// TODO: what if root or path are relative?
	if strings.HasSuffix(root, "/") {
		root = root[:len(root)-1]
	}
	if strings.HasSuffix(path, "/") {
		path = path[:len(path)-1]
	}
	if root == path {
		return 0, nil
	}

	if !strings.HasPrefix(path, root) {
		return 0, fmt.Errorf("%s not a prefix of %s", root, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	if !info.IsDir() {
		path = filepath.Dir(path)
	}

	path = strings.Replace(path, root, "", 1)
	path = strings.Trim(path, "/")
	dirs := strings.Split(path, "/")
	return len(dirs), nil
}

// WalkTree walks the tree starting from root, returning
// all directories and files found. If maxDepth is > 0,
// the walk will truncate below this many levels. Directories
// in the excludeDirs slice will be ignored.
func WalkTree(root string, excludeDirs []string, maxdepth int) ([]string, []string, error) {
	dirs := []string{}
	files := []string{}

	currDepth := func(path string) int {
		depth, _ := DirDepth(root, path)
		return depth
	}

	err := filepath.Walk(
		root,
		func(path string, pathInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !pathInfo.IsDir() {
				files = append(files, path)
			} else {
				if maxdepth > 0 && currDepth(path) > maxdepth {
					return filepath.SkipDir
				}

				for _, e := range excludeDirs {
					if pathInfo.Name() == e {
						return filepath.SkipDir
					}
				}

				dirs = append(dirs, path)
			}

			return nil
		},
	)

	return dirs, files, err
}

// FindDirs finds all directories matching a given dir name
// glob, or exact name, below the given start directory.
// The search goes at most max depth directories down.
func FindDirs(startDir, dirNameGlob string, maxDepth int, ignore []string) ([]string, error) {
	dirs, _, err := WalkTree(startDir, ignore, maxDepth)
	var matches []string
	for _, d := range dirs {
		matched, _ := filepath.Match(dirNameGlob, filepath.Base(d))
		if matched {
			matches = append(matches, d)
		}
	}
	return matches, err
}

// FindFiles finds all files matching a given file name glob, or exact name,
// below the given start directory. The search goes at most max depth
// directories down.
func FindFiles(startDir, fileNameGlob string, maxDepth int, ignore []string) ([]string, error) {
	_, files, err := WalkTree(startDir, ignore, maxDepth)
	var matches []string
	for _, f := range files {
		matched, _ := filepath.Match(fileNameGlob, filepath.Base(f))
		if matched {
			matches = append(matches, f)
		}
	}
	return matches, err
}

// RemoveFiles will delete files matching the given file name glob,
// found at most maxDepth directories below startDir
func RemoveFiles(startDir, fileNameGlob string, maxDepth int, ignore []string) error {
	logging.Error("Removing files!!!")
	files, err := FindFiles(startDir, fileNameGlob, maxDepth, ignore)
	if err != nil {
		logging.Error("oops", logging.F("err", err))
		return err
	}

	logging.Error("files found?", logging.F("n", len(files)))

	for _, file := range files {
		logging.Error(fmt.Sprintf("Removing %s", file))
		os.Remove(file)
	}

	return nil
}
