// Package container keeps the Dockerfile, docker-compose.yml and the container
// CI workflow a deliberate, machine-checked pair.
//
// The container contract used to live in three places that each restated part
// of it: the image, compose, and the CI smoke test. Duplicated values drifted
// (issue #197): compose restated the environment defaults and the healthcheck
// the image already declares, and nothing failed when the two sides disagreed.
// The image now owns the runtime contract and this package is the check that
// makes `go test ./...` fail when another file disagrees with it.
//
// The extraction is deliberately narrow, in the style of internal/references:
// tight regexes and line scans, no YAML library, no `docker compose config`
// subprocess. The fail-loudly promise holds for the shapes listed below; a
// shape outside this list is either parsed loudly, allowed by name with a
// reason, or rejected by name — never guessed at.
//
// Accepted blind spots, stated on purpose:
//   - the runtime stage is everything after the LAST FROM and contributes the
//     whole runtime contract (ENV TKT_*, EXPOSE, USER, STOPSIGNAL, HEALTHCHECK,
//     ENTRYPOINT, the COPY --chown lines); the build stage contributes only
//     the prepared data directory, which the runtime COPY brings into the
//     image by design — every other build-stage line is invisible;
//   - compose is parsed line by line with the repository's fixed indentation
//     (2 for the service, 4 for keys, 6 for list items and nested values);
//     a reformatted or generated compose file fails loudly rather than being
//     guessed at;
//   - exactly one service, one port mapping and one named volume mount are
//     supported; anything else is a parse error, not a silent subset;
//   - compose service keys are allowlisted by name: image, build, ports,
//     volumes, environment, healthcheck and user are parsed; restart,
//     depends_on and networks are ignored because they cannot change the
//     runtime contract; every other key fails loudly and names itself;
//   - the compose file must declare build with context ".": the repository's
//     compose must be able to build the image it names, so an image-only
//     compose file cannot pass this check. A pull-only compose file would
//     need its own consumer entry in the check, not a weakened rule;
//   - compose scalars lose exactly one layer of matching single or double
//     quotes before comparison (image, ports, volume name and target,
//     environment keys and values, user, build context and dockerfile); YAML
//     escapes and anchors are not evaluated;
//   - the compose image reference is split at the first colon, which is exact
//     while the repository path itself contains no colon, and a
//     ${VAR:-default} tag keeps its colon;
//   - Dockerfile ENV values are read verbatim, so build-arg interpolation in
//     those lines would be stored unresolved;
//   - EXPOSE must declare a single port, optionally with a /protocol suffix;
//   - the CI workflow scan looks only at the --env, --volume/-v and
//     --publish/-p flags; among the docker exec invocations it collects, only
//     those running the image's healthcheck binary are selected and the rest
//     are ignored by name (see SelectExecHealthcheck).
package container

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// RepoRoot returns the repository root, found by walking up from this source
// file until a go.mod appears. It mirrors internal/references.RepoRoot.
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0) // only the source path matters here
	if !ok {
		return "", errors.New("cannot locate the container package source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// ImageContract is the runtime contract the Dockerfile declares. Every field
// is required: a reader that cannot find one of them fails instead of
// returning a contract that quietly checks nothing.
type ImageContract struct {
	DBPath         string   // ENV TKT_DB_PATH
	Listen         string   // ENV TKT_LISTEN, host:port
	DataDir        string   // directory the build stage creates and chowns
	ExposePort     string   // EXPOSE, without the optional /protocol suffix
	UserID         string   // USER in the runtime stage, normalized to uid:gid
	StopSignal     string   // STOPSIGNAL in the runtime stage
	EntrypointBin  string   // binary of the runtime ENTRYPOINT JSON exec form
	DataChownUID   string   // chown UID on the prepared data directory
	CopyChownIDs   []string // --chown uid:gid of the runtime COPY lines
	HasHealthcheck bool
	HealthcheckBin string // binary invoked by the HEALTHCHECK CMD
	HealthcheckArg string // first argument of the HEALTHCHECK CMD
}

// ComposeContract is what docker-compose.yml declares for the single service.
// Env, User and HealthcheckTest are optional: their absence is the point of
// the contract, so absence is reported as a zero value, not an error.
type ComposeContract struct {
	ImageRepo       string
	ImageTag        string
	BuildContext    string
	BuildDockerfile string // "" when the build block does not name one
	HostPort        string
	ContainerPort   string
	VolumeName      string
	VolumeTarget    string
	Env             map[string]string // nil when the file declares no environment block
	User            string            // "" when the file declares no user override
	HasHealthcheck  bool
	HealthcheckTest string // raw test value of the healthcheck block
}

// WorkflowContract is the container contract as exercised by the CI smoke
// test in .github/workflows/container.yml.
type WorkflowContract struct {
	DBPath          string      // --env TKT_DB_PATH
	Listen          string      // --env TKT_LISTEN
	VolumeTarget    string      // target side of every --volume/-v flag
	PublishPort     string      // container side of every --publish/-p mapping
	ExecInvocations [][2]string // every docker exec invocation as {binary, argument}
}

// Non-root UID the distroless image and the prepared data directory must use.
// Exported so failure messages and tests can name the expected value.
const NonRootUID = "65532"

var (
	reEnvDBPath      = regexp.MustCompile(`^ENV\s+TKT_DB_PATH=(\S+)\s*$`)
	reEnvListen      = regexp.MustCompile(`^ENV\s+TKT_LISTEN=(\S+)\s*$`)
	rePreparedData   = regexp.MustCompile(`^RUN\s+mkdir\s+-p\s+(\S+)\s+&&\s+chown\s+(\d+):(\d+)\s+(\S+)\s*$`)
	reExpose         = regexp.MustCompile(`^EXPOSE\s+(\d+)(?:/\S+)?\s*$`)
	reRuntimeUser    = regexp.MustCompile(`^USER\s+(\d+)(?::(\d+))?\s*$`)
	reStopSignal     = regexp.MustCompile(`^STOPSIGNAL\s+(\S+)\s*$`)
	reEntrypoint     = regexp.MustCompile(`^ENTRYPOINT\s+\[\s*"([^"]+)"`)
	reCopyChown      = regexp.MustCompile(`^COPY\s+.*--chown=(\d+):(\d+)`)
	reHealthcheckCmd = regexp.MustCompile(`CMD\s+\[\s*"([^"]+)"\s*,\s*"([^"]+)"\s*\]`)
	// The tag is split at the FIRST colon: a repository path never contains
	// one, while a ${VAR:-default} tag does.
	reComposeImage  = regexp.MustCompile(`^([^:\s]+):(.+)$`)
	reComposePort   = regexp.MustCompile(`^(\d+):(\d+)$`)
	reComposeVolume = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*):(/\S+)$`)
	reComposeEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reComposeEnvVal = regexp.MustCompile(`^([^=\s]+)=(\S.*)$`)
	// --env TKT_*: the value must be space-free, so line continuations in the
	// workflow's run blocks stay out of the match.
	reWorkflowEnv = regexp.MustCompile(`--env\s+(TKT_DB_PATH|TKT_LISTEN)=(\S+)`)
	// The volume source may be a GitHub expression with spaces inside ${{ ... }},
	// so the source is any unquoted run up to the first colon; only the target
	// side must be space-free. Both the long and the short flag are scanned,
	// because a regression can hide in either.
	reWorkflowVolume  = regexp.MustCompile(`(?:--volume|-v)\s+"?([^":]+):([^"\s]+)"?`)
	reWorkflowPublish = regexp.MustCompile(`(?:--publish|-p)\s+"?([^"\s]+)"?`)
	// The exec invocations: docker exec <container> <bin> <arg>, where the
	// container name may be a quoted GitHub expression and the shell may
	// terminate the last token with a semicolon. Selection of the healthcheck
	// among them is SelectExecHealthcheck's job, not the regex's.
	reWorkflowExec = regexp.MustCompile(`docker exec\s+("[^"]*"|\S+)\s+([^\s;]+)\s+([^\s;]+)`)
)

// composeServiceKeysAllowed is the explicit allowlist of compose service keys.
// Parsed keys carry contract data; the ignored ones are ignored BY NAME with a
// reason, because they cannot change the runtime contract. Every other key
// fails loudly: an unlisted key can silently bypass the image contract (for
// example command, entrypoint or env_file).
var composeServiceKeysAllowed = map[string]string{
	"image":       "parsed: the published repository and tag",
	"build":       "parsed: context and dockerfile, so compose cannot point the check at a Dockerfile it never read",
	"ports":       "parsed: the host and container mapping",
	"volumes":     "parsed: the named data volume and its target",
	"environment": "parsed: block and list spellings, compared against the image ENV defaults",
	"healthcheck": "parsed: the test sub-key only, and only a restatement identical to the image passes — every other sub-key is rejected because the image owns the healthcheck",
	"user":        "parsed: must not override the image USER",
	"restart":     "ignored by name: a restart policy cannot change the runtime contract",
	"depends_on":  "ignored by name: start ordering cannot change the runtime contract",
	"networks":    "ignored by name: network membership cannot change the runtime contract",
}

// ReadDockerfile reads and parses the repository Dockerfile.
func ReadDockerfile(root string) (ImageContract, error) {
	body, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		return ImageContract{}, fmt.Errorf("read Dockerfile: %w", err)
	}
	return parseDockerfile(body)
}

// ReadCompose reads and parses docker-compose.yml.
func ReadCompose(root string) (ComposeContract, error) {
	body, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		return ComposeContract{}, fmt.Errorf("read docker-compose.yml: %w", err)
	}
	return parseCompose(body)
}

// ReadWorkflow reads and parses the container CI workflow.
func ReadWorkflow(root string) (WorkflowContract, error) {
	body, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "container.yml"))
	if err != nil {
		return WorkflowContract{}, fmt.Errorf("read .github/workflows/container.yml: %w", err)
	}
	return parseWorkflow(body)
}

// parseDockerfile extracts the image contract. The runtime stage is everything
// after the LAST FROM and it contributes the whole runtime contract: ENV
// TKT_*, EXPOSE, USER, STOPSIGNAL, HEALTHCHECK, ENTRYPOINT and the COPY
// --chown lines. The build stage contributes only the prepared data directory,
// which the runtime COPY brings into the image by design; every other
// build-stage line is invisible to the contract. A value found twice with
// different values is a parse error, never a last-one-wins, because that
// ambiguity is exactly what a contract must not have.
func parseDockerfile(body []byte) (ImageContract, error) {
	var c ImageContract
	lines := strings.Split(string(body), "\n")

	lastFrom := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "FROM") {
			lastFrom = i
		}
	}

	sawHealthcheck, sawEntrypoint := false, false
	for i, line := range lines {
		where := fmt.Sprintf("Dockerfile:%d", i+1)
		runtimeStage := i > lastFrom

		if !runtimeStage {
			// Build stage (everything up to and including the FROM line, whose
			// text can never match the regex): only the prepared data
			// directory is contract data; everything else is invisible.
			if m := rePreparedData.FindStringSubmatch(line); m != nil {
				if m[1] != m[4] {
					return c, fmt.Errorf("%s: mkdir -p %s and chown target %s disagree", where, m[1], m[4])
				}
				if c.DataDir != "" && c.DataDir != m[1] {
					return c, fmt.Errorf("%s: two data directories prepared (%s, %s)", where, c.DataDir, m[1])
				}
				c.DataDir, c.DataChownUID = m[1], m[2]
			}
			continue
		}

		if m := reEnvDBPath.FindStringSubmatch(line); m != nil {
			if c.DBPath != "" && c.DBPath != m[1] {
				return c, fmt.Errorf("%s: TKT_DB_PATH declared twice with different values (%s, %s)", where, c.DBPath, m[1])
			}
			c.DBPath = m[1]
		}
		if m := reEnvListen.FindStringSubmatch(line); m != nil {
			if c.Listen != "" && c.Listen != m[1] {
				return c, fmt.Errorf("%s: TKT_LISTEN declared twice with different values (%s, %s)", where, c.Listen, m[1])
			}
			c.Listen = m[1]
		}
		if m := reExpose.FindStringSubmatch(line); m != nil {
			if c.ExposePort != "" && c.ExposePort != m[1] {
				return c, fmt.Errorf("%s: two EXPOSE ports (%s, %s)", where, c.ExposePort, m[1])
			}
			c.ExposePort = m[1]
		}
		if m := reRuntimeUser.FindStringSubmatch(line); m != nil {
			gid := m[2]
			if gid == "" {
				gid = m[1] // USER <uid> means the gid equals the uid
			}
			uid := m[1] + ":" + gid
			if c.UserID != "" && c.UserID != uid {
				return c, fmt.Errorf("%s: two runtime USER values (%s, %s)", where, c.UserID, uid)
			}
			c.UserID = uid
		}
		if m := reStopSignal.FindStringSubmatch(line); m != nil {
			if c.StopSignal != "" && c.StopSignal != m[1] {
				return c, fmt.Errorf("%s: two STOPSIGNAL values (%s, %s)", where, c.StopSignal, m[1])
			}
			c.StopSignal = m[1]
		}
		if m := reCopyChown.FindStringSubmatch(line); m != nil {
			c.CopyChownIDs = append(c.CopyChownIDs, m[1]+":"+m[2])
		}
		if strings.HasPrefix(line, "HEALTHCHECK") {
			sawHealthcheck = true
			if m := reHealthcheckCmd.FindStringSubmatch(line); m != nil {
				if c.HasHealthcheck && (c.HealthcheckBin != m[1] || c.HealthcheckArg != m[2]) {
					return c, fmt.Errorf("%s: two HEALTHCHECK commands (%s %s, %s %s)", where, c.HealthcheckBin, c.HealthcheckArg, m[1], m[2])
				}
				c.HasHealthcheck, c.HealthcheckBin, c.HealthcheckArg = true, m[1], m[2]
			}
		}
		if strings.HasPrefix(line, "ENTRYPOINT") {
			sawEntrypoint = true
			if m := reEntrypoint.FindStringSubmatch(line); m != nil {
				if c.EntrypointBin != "" && c.EntrypointBin != m[1] {
					return c, fmt.Errorf("%s: two ENTRYPOINT binaries (%s, %s)", where, c.EntrypointBin, m[1])
				}
				c.EntrypointBin = m[1]
			}
		}
	}

	// Non-vacuity: a missing value means the extractor found nothing, which
	// must fail rather than let the invariants pass on empty strings. Every
	// missing field is named, so one broken extractor cannot hide the others.
	var missing []string
	for _, field := range []struct {
		name, value string
	}{
		{"ENV TKT_DB_PATH", c.DBPath},
		{"ENV TKT_LISTEN", c.Listen},
		{"prepared data directory (mkdir/chown)", c.DataDir},
		{"EXPOSE port", c.ExposePort},
		{"runtime USER", c.UserID},
		{"STOPSIGNAL", c.StopSignal},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if !sawHealthcheck {
		missing = append(missing, "HEALTHCHECK directive")
	} else if !c.HasHealthcheck {
		missing = append(missing, "HEALTHCHECK in JSON exec form (shell form found)")
	}
	if !sawEntrypoint {
		missing = append(missing, "runtime ENTRYPOINT")
	} else if c.EntrypointBin == "" {
		missing = append(missing, "runtime ENTRYPOINT in JSON exec form (shell form found)")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("could not extract %s from the Dockerfile: the contract is unreadable, never skippable", strings.Join(missing, ", "))
	}
	if c.EntrypointBin != c.HealthcheckBin {
		return c, fmt.Errorf("the Dockerfile runtime ENTRYPOINT runs %q but the HEALTHCHECK runs %q: compose cannot override either, so both must invoke the same binary", c.EntrypointBin, c.HealthcheckBin)
	}
	return c, nil
}

// unquote strips exactly one layer of matching single or double quotes from a
// compose scalar value. YAML escapes inside the quotes are NOT evaluated.
func unquote(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

// parseCompose extracts the service contract with the fixed indentation shape
// documented in the package comment. Service keys are allowlisted by name;
// parsed keys carry contract data, three benign keys are ignored with a stated
// reason, and every other key fails loudly. Lines that look like data this
// parser claims to own but cannot read are errors.
func parseCompose(body []byte) (ComposeContract, error) {
	var c ComposeContract
	section, sub := "", ""
	inService := false
	services := 0

	for i, line := range strings.Split(string(body), "\n") {
		where := fmt.Sprintf("docker-compose.yml:%d", i+1)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		switch {
		case indent == 0:
			section, sub, inService = strings.TrimSuffix(trimmed, ":"), "", false

		case section == "services" && indent == 2 && strings.HasSuffix(trimmed, ":"):
			services++
			if services > 1 {
				return c, fmt.Errorf("%s: found a second service %q; this check supports exactly one", where, strings.TrimSuffix(trimmed, ":"))
			}
			inService, sub = true, ""

		case section == "services" && inService && indent == 4:
			key, rest, _ := strings.Cut(trimmed, ":")
			key, rest = strings.TrimSpace(key), strings.TrimSpace(rest)
			sub = key
			reason, allowed := composeServiceKeysAllowed[key]
			if !allowed {
				return c, fmt.Errorf("%s: service key %q is not in the container check's allowlist. Compose keys can silently bypass the image contract: command and entrypoint override what the container runs, env_file injects environment values the check cannot read, build.dockerfile builds an image the check never read, stop_signal overrides the image STOPSIGNAL, and privileged, cap_add or security_opt defeat the non-root runtime. If this key is genuinely benign, allow it deliberately in composeServiceKeysAllowed with a reason", where, key)
			}
			switch {
			case key == "image":
				m := reComposeImage.FindStringSubmatch(unquote(rest))
				if m == nil {
					return c, fmt.Errorf("%s: image %q has no tag; the published target must be repository:tag", where, rest)
				}
				c.ImageRepo, c.ImageTag = m[1], m[2]
			case key == "user":
				c.User = unquote(rest)
			case key == "healthcheck":
				c.HasHealthcheck = true
			case key == "build":
				// Inline context form (`build: .`); the block form is handled
				// under sub == "build" below.
				if rest != "" {
					c.BuildContext = unquote(rest)
				}
			case key == "ports", key == "volumes", key == "environment":
				if rest != "" {
					return c, fmt.Errorf("%s: inline %q is not supported; write it as a block", where, key)
				}
			default:
				_ = reason // allowed and benign: restart, depends_on, networks
			}

		case section == "services" && inService && indent >= 6 && strings.HasPrefix(trimmed, "- "):
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			switch sub {
			case "ports":
				m := reComposePort.FindStringSubmatch(unquote(item))
				if m == nil {
					return c, fmt.Errorf("%s: port mapping %q is unreadable; expected \"host:container\"", where, item)
				}
				if c.ContainerPort != "" && c.ContainerPort != m[2] {
					return c, fmt.Errorf("%s: two container ports (%s, %s)", where, c.ContainerPort, m[2])
				}
				c.HostPort, c.ContainerPort = m[1], m[2]
			case "volumes":
				m := reComposeVolume.FindStringSubmatch(unquote(item))
				if m == nil {
					return c, fmt.Errorf("%s: volume %q is unreadable; expected \"name:/target\" with an absolute target", where, item)
				}
				if c.VolumeTarget != "" && c.VolumeTarget != m[2] {
					return c, fmt.Errorf("%s: two volume targets (%s, %s)", where, c.VolumeTarget, m[2])
				}
				c.VolumeName, c.VolumeTarget = m[1], m[2]
			case "environment":
				// The list spelling of environment (- KEY=value) carries the
				// same contract weight as the block spelling, so it must land
				// in Env instead of falling through unnoticed.
				m := reComposeEnvVal.FindStringSubmatch(item)
				if m == nil {
					return c, fmt.Errorf("%s: environment entry %q is unreadable; expected \"KEY=value\"", where, item)
				}
				if c.Env == nil {
					c.Env = map[string]string{}
				}
				c.Env[unquote(m[1])] = unquote(m[2])
			}

		case section == "services" && inService && indent >= 6:
			key, rest, _ := strings.Cut(trimmed, ":")
			key, rest = strings.TrimSpace(key), strings.TrimSpace(rest)
			switch sub {
			case "environment":
				if reComposeEnvKey.FindStringSubmatch(unquote(key)) == nil || rest == "" {
					return c, fmt.Errorf("%s: environment entry %q is unreadable; expected \"KEY: value\"", where, trimmed)
				}
				if c.Env == nil {
					c.Env = map[string]string{}
				}
				c.Env[unquote(key)] = unquote(rest)
			case "healthcheck":
				// The image owns the healthcheck: compose may restate the same
				// command, but retuning intervals or disabling it would make
				// compose a second source of truth the image cannot see.
				switch key {
				case "test":
					c.HealthcheckTest = strings.TrimSpace(rest)
				default:
					return c, fmt.Errorf("%s: healthcheck key %q is rejected: the image owns the healthcheck, so compose must not retune or disable it; remove the key or extend the check deliberately", where, key)
				}
			case "build":
				switch key {
				case "context":
					c.BuildContext = unquote(rest)
				case "dockerfile":
					c.BuildDockerfile = unquote(rest)
				default:
					return c, fmt.Errorf("%s: build key %q is not in the container check's allowlist; build keys can change what is built (args, target, additional contexts). If it is genuinely benign, allow it deliberately in the check with a reason", where, key)
				}
			}
		}
	}

	// Non-vacuity, same rule as the Dockerfile reader.
	var missing []string
	for _, field := range []struct {
		name, value string
	}{
		{"image repository", c.ImageRepo},
		{"image tag", c.ImageTag},
		{"build context", c.BuildContext},
		{"host port", c.HostPort},
		{"container port", c.ContainerPort},
		{"named volume name", c.VolumeName},
		{"named volume target", c.VolumeTarget},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("docker-compose.yml: could not extract the service %s: the contract is unreadable, never skippable", strings.Join(missing, ", "))
	}
	// One leading ./ is a spelling of the same target, not another tree; the
	// bare "./" spelling is exactly ".".
	normalize := func(v string) string {
		if v == "./" {
			return "."
		}
		return strings.TrimPrefix(v, "./")
	}
	if normalize(c.BuildContext) != "." {
		return c, fmt.Errorf("docker-compose.yml: build context is %q, want \".\": compose must not point the check at another tree", c.BuildContext)
	}
	if c.BuildDockerfile != "" && normalize(c.BuildDockerfile) != "Dockerfile" {
		return c, fmt.Errorf("docker-compose.yml: build.dockerfile is %q, want \"Dockerfile\": compose must not build a Dockerfile the check never read", c.BuildDockerfile)
	}
	if c.HasHealthcheck && c.HealthcheckTest == "" {
		return c, errors.New("docker-compose.yml: healthcheck block without a readable test value")
	}
	return c, nil
}

// parseWorkflow extracts every --env, --volume/-v and --publish/-p flag of the
// CI smoke test, plus every docker exec invocation. Multiple occurrences
// (first start, restart, busybox probes) must all agree; one disagreeing flag
// fails, because the workflow is a consumer of the contract and half-agreeing
// is still drifting. Which exec invocation is the healthcheck is decided by
// SelectExecHealthcheck against the image contract, not here.
func parseWorkflow(body []byte) (WorkflowContract, error) {
	var c WorkflowContract
	text := string(body)

	envs := reWorkflowEnv.FindAllStringSubmatch(text, -1)
	vols := reWorkflowVolume.FindAllStringSubmatch(text, -1)
	pubs := reWorkflowPublish.FindAllStringSubmatch(text, -1)
	execs := reWorkflowExec.FindAllStringSubmatch(text, -1)
	if len(envs) == 0 {
		return c, errors.New(".github/workflows/container.yml: no --env TKT_* flags found: the smoke test is unreadable, never skippable")
	}
	if len(vols) == 0 {
		return c, errors.New(".github/workflows/container.yml: no --volume or -v flags found: the smoke test is unreadable, never skippable")
	}
	if len(pubs) == 0 {
		return c, errors.New(".github/workflows/container.yml: no --publish or -p flags found: the smoke test is unreadable, never skippable")
	}
	if len(execs) == 0 {
		return c, errors.New(".github/workflows/container.yml: no docker exec invocation found: the smoke test is unreadable, never skippable")
	}

	for _, m := range envs {
		switch m[1] {
		case "TKT_DB_PATH":
			if c.DBPath != "" && c.DBPath != m[2] {
				return c, fmt.Errorf(".github/workflows/container.yml: TKT_DB_PATH passed twice with different values (%s, %s)", c.DBPath, m[2])
			}
			c.DBPath = m[2]
		case "TKT_LISTEN":
			if c.Listen != "" && c.Listen != m[2] {
				return c, fmt.Errorf(".github/workflows/container.yml: TKT_LISTEN passed twice with different values (%s, %s)", c.Listen, m[2])
			}
			c.Listen = m[2]
		}
	}
	for _, m := range vols {
		if c.VolumeTarget != "" && c.VolumeTarget != m[2] {
			return c, fmt.Errorf(".github/workflows/container.yml: volumes mounted at two targets (%s, %s)", c.VolumeTarget, m[2])
		}
		c.VolumeTarget = m[2]
	}
	for _, m := range pubs {
		port, err := publishContainerPort(m[1])
		if err != nil {
			return c, fmt.Errorf(".github/workflows/container.yml: publish mapping %q is unreadable: %v", m[1], err)
		}
		if c.PublishPort != "" && c.PublishPort != port {
			return c, fmt.Errorf(".github/workflows/container.yml: two container ports published (%s, %s)", c.PublishPort, port)
		}
		c.PublishPort = port
	}
	for _, m := range execs {
		c.ExecInvocations = append(c.ExecInvocations, [2]string{m[2], m[3]})
	}

	// Non-vacuity, same rule as the other readers.
	var missing []string
	for _, field := range []struct {
		name, value string
	}{
		{"--env TKT_DB_PATH", c.DBPath},
		{"--env TKT_LISTEN", c.Listen},
		{"--volume/-v target", c.VolumeTarget},
		{"--publish/-p container port", c.PublishPort},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf(".github/workflows/container.yml: could not extract %s: the contract is unreadable, never skippable", strings.Join(missing, ", "))
	}
	return c, nil
}

// SelectExecHealthcheck returns the docker exec invocation that runs the
// image's healthcheck binary. Other docker exec invocations (diagnostics,
// probes) are ignored by name. It fails when no invocation runs the image's
// healthcheck binary, or when the matched invocation's argument disagrees with
// the image HEALTHCHECK.
func SelectExecHealthcheck(img ImageContract, wf WorkflowContract) ([2]string, error) {
	matched := 0
	var selected [2]string
	for _, e := range wf.ExecInvocations {
		if e[0] != img.HealthcheckBin {
			continue // ignored by name: diagnostics and probes are not the healthcheck
		}
		matched++
		selected = e
		if e[1] != img.HealthcheckArg {
			return selected, fmt.Errorf(".github/workflows/container.yml: exec healthcheck runs %s %s but the image HEALTHCHECK runs %s %s", e[0], e[1], img.HealthcheckBin, img.HealthcheckArg)
		}
	}
	if matched == 0 {
		return selected, fmt.Errorf(".github/workflows/container.yml: no docker exec invocation runs the image healthcheck binary %s", img.HealthcheckBin)
	}
	return selected, nil
}

// publishContainerPort returns the container side of a docker publish mapping,
// accepting [ip:]host:container with an optional /protocol suffix.
func publishContainerPort(mapping string) (string, error) {
	hostPart, _, _ := strings.Cut(mapping, "/")
	parts := strings.Split(hostPart, ":")
	switch len(parts) {
	case 2:
		return parts[1], nil
	case 3:
		return parts[2], nil
	}
	return "", errors.New("expected [ip:]host:container")
}

// ModulePath returns the go.mod module path. The check derives the expected
// GHCR repository from it instead of restating the image name in code.
func ModulePath(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	re := regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	m := re.FindStringSubmatch(string(body))
	if m == nil {
		return "", errors.New("go.mod: could not extract the module path")
	}
	return m[1], nil
}
