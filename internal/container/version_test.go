package container

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/version"
)

func TestInstanceVersionResolution(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "source-revision"}}}
	for _, tc := range []struct {
		name, release, revision, want string
		info                          *debug.BuildInfo
	}{
		{"release wins", "v1.2.3", "injected-revision", "v1.2.3", info},
		{"container revision", "dev", "injected-revision", "injected-revision", info},
		{"source revision", "dev", "unknown", "source-revision", info},
		{"empty defaults", "", "", "source-revision", info},
		{"source archive", "dev", "unknown", "dev", nil},
		{"missing vcs", "dev", "unknown", "dev", &debug.BuildInfo{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := version.Resolve(tc.release, tc.revision, tc.info); got != tc.want {
				t.Errorf("version = %q, want %q", got, tc.want)
			}
		})
	}
}

// Release args must reach the executable, not merely the OCI labels. This
// intentionally guards the repository's single-line build command and explicit
// ARG declarations; alternative build shapes require a deliberate check update.
func TestInstanceVersionBuildContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	build, runtime, ok := strings.Cut(text, "FROM gcr.io/distroless/")
	if !ok {
		t.Fatal("runtime stage not found")
	}
	for _, arg := range []string{"ARG VERSION=dev", "ARG REVISION=unknown"} {
		if !strings.Contains(build, arg+"\n") || !strings.Contains(runtime, arg+"\n") {
			t.Errorf("%s must be declared in build and runtime stages", arg)
		}
	}
	var command string
	for _, line := range strings.Split(build, "\n") {
		if strings.HasPrefix(line, "RUN CGO_ENABLED=0 go build ") {
			command = line
		}
	}
	for _, arg := range []string{"ARG VERSION=dev", "ARG REVISION=unknown"} {
		if command != "" && strings.Index(build, arg+"\n") > strings.Index(build, command) {
			t.Errorf("%s must precede the server build", arg)
		}
	}
	for _, flag := range []string{
		"-X github.com/giulianotesta7/tkt/internal/version.Release=$VERSION",
		"-X github.com/giulianotesta7/tkt/internal/version.Revision=$REVISION",
	} {
		if !strings.Contains(command, flag) {
			t.Errorf("server build missing %q", flag)
		}
	}
}
