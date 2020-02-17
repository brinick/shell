package shell

import (
	"fmt"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestTimeoutOption(t *testing.T) {
	hook := logtest.NewGlobal()
	log.AddHook(hook)
	result := Run("sleep 2", Timeout(200*time.Millisecond))
	if !result.TimedOut {
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
	if !result.Cancelled {
		t.Error("Expected process to be marked cancelled")
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
