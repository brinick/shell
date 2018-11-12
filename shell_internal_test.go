package shell

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"reflect"
	"testing"
)

func TestBuildCmdArgs(t *testing.T) {
	tt := []struct {
		name string
		cmd  string
		exe  string
		args []string
	}{
		{"no args", "ls", "ls", []string{}},
		{"one arg", "ls arg1", "ls", []string{"arg1"}},
		{"multiple args", "ls -ltr arg1", "ls", []string{"-ltr", "arg1"}},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			exe, args := buildCmdArgs(tc.cmd)
			if exe != tc.exe {
				t.Errorf("Expected %v, got %v", tc.exe, exe)
			}

			if !(len(tc.args) == 0 && len(args) == 0) {
				if !reflect.DeepEqual(args, tc.args) {
					t.Errorf("Expected %v, got %v", tc.args, args)
				}
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	hook := logtest.NewGlobal()
	log.AddHook(hook)
	result := Run("sleep 2", Timeout(1))
	if !result.Killed {
		t.Errorf("Expected process to be killed, but it was not.")
	}
}

func TestEnv(t *testing.T) {
	before := Run("env")
	after := Run("env", Env([]string{"HIP_HIP=hooray"}))
	missing := missingEntries(after.Stdout.Lines(), before.Stdout.Lines())
	for _, m := range missing {
		fmt.Println(m)
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
