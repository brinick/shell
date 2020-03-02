package shell

import (
	"fmt"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestTimeoutOption(t *testing.T) {
	hook := logtest.NewGlobal()
	log.AddHook(hook)
	result := Run("sleep 2", Timeout(200*time.Millisecond))
	if !result.TimedOut() {
		t.Error("Expected process to be marked timed out")
		t.Log(result)
	}
}

func TestEnvOption(t *testing.T) {
	before := Run("env")
	after := Run("env", Env([]string{"HIP_HIP=hooray"}))
	missing := missingEntries(
		after.Stdout().Lines(),
		before.Stdout().Lines(),
	)
	for _, m := range missing {
		fmt.Println(m)
	}
}

func TestCancelOption(t *testing.T) {
	stopChan := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(stopChan)
	}()
	result := Run("sleep 1", Cancel(stopChan))
	if !result.canceled {
		t.Error("Expected process to be marked canceled")
	}
}

func TestBkgdOption(t *testing.T) {
	res := Run("echo 'hello';sleep 1;echo 'world';", Bkgd())
	if res.IsReady() {
		t.Error("Background process should still be running")
	}

	for !res.IsReady() {
	}

	t.Log(res.Stdout().Text())
	t.Log(res.Stderr().Text())
}

func TestStdOutputs(t *testing.T) {
	tests := []struct {
		name   string
		cmd    string
		expect []string
	}{
		{"piping all output to devnull", "ls >& /dev/null", []string{}},
		{"echo hello", "echo 'hello'", []string{"hello"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Run(tt.cmd)
			got := res.Stdout()
			expectEmpty := len(tt.expect) == 0
			gotEmpty := got.Empty()

			switch expectEmpty {
			case true:
				if !gotEmpty {
					t.Errorf(
						"%s: expected empty output, but got %d output lines:\n%s",
						tt.name,
						len(got.Lines()),
						got.Text(),
					)
				}

			case false:
				if gotEmpty {
					t.Errorf("%s: did not expect empty output, but got it", tt.name)
				} else {
					// do we get the expected output?
					want := strings.Join(tt.expect, "\n")
					if got.Text() != want {
						t.Errorf(
							"%s: expected and received output mismatch.\n=>Expected:\n%s\n=> Got:\n%s",
							tt.name,
							want,
							got.Text(),
						)
					}

				}
			}
		})
	}

}

func TestErrorValues(t *testing.T) {
	tests := []struct {
		name   string
		cmd    string
		expect error
	}{
		{"running simple ls", "ls >& /dev/null", nil},

		// non-nil error is only returned if there was an issue executing
		// the command, not just that the command - as here - returns a non-zero exitcode
		{"running unknown command", "lssss >& /dev/null", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Run(tt.cmd).Error()
			if got != tt.expect {
				t.Errorf("%s: expected exit code %v, got %v\n", tt.name, tt.expect, got)
			}
		})
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		cmd    string
		expect int
	}{
		{"running simple ls", "ls >& /dev/null", 0},
		{"running unknown command", "lssss >& /dev/null", 127},
		{"exit explicitly with 1", "exit 1;", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Run(tt.cmd).ExitCode()
			if got != tt.expect {
				t.Errorf("%s: expected exit code %d, got %d\n", tt.name, tt.expect, got)
			}
		})
	}
}

// missingEntries returns the entries in a not in b
func missingEntries(a, b []string) []string {
	mb := map[string]bool{}
	for _, entry := range a {
		mb[entry] = true
	}

	missing := []string{}
	for _, entry := range a {
		if _, found := mb[entry]; !found {
			missing = append(missing, entry)
		}
	}

	return missing
}
