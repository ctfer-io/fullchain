package smoke_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

func Test_S_Smoke(t *testing.T) {
	// This test simply checks the CTFer component could be deployed.
	// It does not try to reach out a service, which might be a future work.

	pwd, _ := os.Getwd()
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Quick:       true,
		SkipRefresh: true,
		Dir:         filepath.Join(pwd, ".."),
		StackName:   stackName(t.Name()),
		Config: map[string]string{
			// Redefine the CTFer's platform requests so it is compatible with in-CI restrictions.
			"ctfer-platform-requests-cpu":    "200m",  // we might be too short for more
			"ctfer-platform-requests-memory": "256Mi", // here we have plenty space
		},
		Env: []string{
			fmt.Sprintf("GOCOVERDIR=%s", filepath.Join(pwd, "..", "coverdir")),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			time.Sleep(time.Minute)
		},
	})
}

func stackName(tname string) (out string) {
	out = tname
	out = strings.TrimPrefix(out, "Test_S_")
	out = strings.ToLower(out)
	return out
}
