package container

import (
	"fmt"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func mustReadAll(t *testing.T, root string) (ImageContract, ComposeContract, WorkflowContract) {
	t.Helper()
	img, err := ReadDockerfile(root)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	comp, err := ReadCompose(root)
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	wf, err := ReadWorkflow(root)
	if err != nil {
		t.Fatalf("read .github/workflows/container.yml: %v", err)
	}
	return img, comp, wf
}

func mustReadRelease(t *testing.T, root string) ReleaseContract {
	t.Helper()
	rel, err := ReadReleaseWorkflow(root)
	if err != nil {
		t.Fatalf("read .github/workflows/release-container.yml: %v", err)
	}
	return rel
}

// TestContractsParse is the non-vacuity gate: every extractor must actually
// find the values it claims to extract. If a reader returns empty fields, the
// invariants below would pass while checking nothing.
func TestContractsParse(t *testing.T) {
	root := repoRoot(t)
	img, comp, wf := mustReadAll(t, root)
	rel := mustReadRelease(t, root)

	if img.DBPath != "/data/tkt.db" {
		t.Errorf("Dockerfile TKT_DB_PATH = %q, want /data/tkt.db", img.DBPath)
	}
	if img.Listen != ":8080" {
		t.Errorf("Dockerfile TKT_LISTEN = %q, want :8080", img.Listen)
	}
	if img.DataDir == "" || img.DataChownUID == "" {
		t.Errorf("Dockerfile prepared data directory = %q (chown UID %q): the mkdir/chown extractor found nothing", img.DataDir, img.DataChownUID)
	}
	if img.ExposePort == "" {
		t.Errorf("Dockerfile EXPOSE is empty: the extractor found nothing")
	}
	if img.UserID == "" {
		t.Errorf("Dockerfile runtime USER is empty: the extractor found nothing")
	}
	if img.StopSignal == "" {
		t.Errorf("Dockerfile STOPSIGNAL is empty: the extractor found nothing")
	}
	if img.EntrypointBin == "" {
		t.Errorf("Dockerfile runtime ENTRYPOINT is empty: the extractor found nothing")
	}
	if !img.HasHealthcheck || img.HealthcheckBin == "" || img.HealthcheckArg == "" {
		t.Errorf("Dockerfile HEALTHCHECK = %v (%s %s): the extractor found nothing", img.HasHealthcheck, img.HealthcheckBin, img.HealthcheckArg)
	}
	if len(img.CopyChownIDs) == 0 {
		t.Errorf("Dockerfile has no COPY --chown lines: the volume-ownership extractor found nothing")
	}
	if comp.ImageRepo == "" || comp.ImageTag == "" {
		t.Errorf("compose image = %q:%q: the extractor found nothing", comp.ImageRepo, comp.ImageTag)
	}
	if comp.BuildContext == "" {
		t.Errorf("compose build context is empty: the extractor found nothing")
	}
	if comp.HostPort == "" || comp.ContainerPort == "" {
		t.Errorf("compose ports = %s->%s: the extractor found nothing", comp.HostPort, comp.ContainerPort)
	}
	if comp.VolumeName == "" || comp.VolumeTarget == "" {
		t.Errorf("compose volume = %q at %q: the extractor found nothing", comp.VolumeName, comp.VolumeTarget)
	}
	if wf.DBPath == "" || wf.Listen == "" || wf.VolumeTarget == "" {
		t.Errorf("workflow contract = %v: the --env/--volume extractor found nothing", wf)
	}
	if wf.PublishPort == "" {
		t.Errorf("workflow publish port is empty: the --publish/-p extractor found nothing")
	}
	if len(wf.ExecInvocations) == 0 {
		t.Errorf("workflow exec invocations are empty: the docker exec extractor found nothing")
	}
	if !rel.ImageRefDerived {
		t.Errorf("release workflow image reference is not derived from ${GITHUB_REPOSITORY,,}: the extractor found nothing")
	}
	if !rel.HasVersionArg || !rel.HasRevisionArg {
		t.Errorf("release workflow build args = VERSION:%v REVISION:%v: the build-arg extractor found nothing", rel.HasVersionArg, rel.HasRevisionArg)
	}
	if rel.Smoke.VolumeTarget == "" || rel.Smoke.PublishPort == "" || len(rel.Smoke.ExecInvocations) == 0 {
		t.Errorf("release smoke contract = %+v: the flag extractor found nothing", rel.Smoke)
	}
}

// TestDataPathContract is invariant I1: the database path must live under the
// directory the Dockerfile prepares and chowns, and the compose volume must
// mount exactly that directory. This is the invariant whose absence produced a
// container restart loop on a fresh named volume.
func TestDataPathContract(t *testing.T) {
	img, comp, _ := mustReadAll(t, repoRoot(t))

	if !strings.HasPrefix(img.DBPath, img.DataDir+"/") || img.DBPath == img.DataDir {
		t.Errorf("I1: Dockerfile TKT_DB_PATH %q is not a file inside the prepared directory %q", img.DBPath, img.DataDir)
	}
	if img.DataChownUID != NonRootUID {
		t.Errorf("I1: Dockerfile chowns %s to UID %s, want %s: a fresh named volume would be unwritable", img.DataDir, img.DataChownUID, NonRootUID)
	}
	if comp.VolumeTarget != img.DataDir {
		t.Errorf("I1: docker-compose.yml mounts volume %q at %q but the Dockerfile prepared %q", comp.VolumeName, comp.VolumeTarget, img.DataDir)
	}
}

// TestPortContract is invariant I2: EXPOSE, the TKT_LISTEN port and the
// container side of the compose mapping must be the same number.
func TestPortContract(t *testing.T) {
	img, comp, _ := mustReadAll(t, repoRoot(t))

	_, listenPort, found := strings.Cut(img.Listen, ":")
	if !found || listenPort == "" {
		t.Fatalf("I2: Dockerfile TKT_LISTEN %q has no readable port", img.Listen)
	}
	if img.ExposePort != listenPort {
		t.Errorf("I2: Dockerfile EXPOSE %s and TKT_LISTEN port %s disagree", img.ExposePort, listenPort)
	}
	if comp.ContainerPort != listenPort {
		t.Errorf("I2: docker-compose.yml maps to container port %s but the image listens on %s (EXPOSE %s)", comp.ContainerPort, listenPort, img.ExposePort)
	}
}

// TestHealthcheckOwnership is invariant I3: the image declares the healthcheck
// and compose inherits it. Compose may restate the same command, but a
// contradicting one is drift.
func TestHealthcheckOwnership(t *testing.T) {
	img, comp, _ := mustReadAll(t, repoRoot(t))

	if img.HealthcheckBin != "/server" || img.HealthcheckArg != "-healthcheck" {
		t.Errorf("I3: Dockerfile HEALTHCHECK runs %q %q, want /server -healthcheck", img.HealthcheckBin, img.HealthcheckArg)
	}
	if !comp.HasHealthcheck {
		return // absence passes: the image owns the healthcheck
	}
	if !strings.Contains(comp.HealthcheckTest, img.HealthcheckBin) || !strings.Contains(comp.HealthcheckTest, img.HealthcheckArg) {
		t.Errorf("I3: docker-compose.yml healthcheck %q contradicts the image healthcheck %q %q", comp.HealthcheckTest, img.HealthcheckBin, img.HealthcheckArg)
	}
}

// TestNonRootContract is invariant I4: the runtime stage runs as the non-root
// UID, stops with SIGTERM, the prepared directory and every runtime COPY carry
// the same uid:gid, and compose does not override the user with a different
// one.
func TestNonRootContract(t *testing.T) {
	img, comp, _ := mustReadAll(t, repoRoot(t))

	if img.UserID != NonRootUID+":"+NonRootUID {
		t.Errorf("I4: Dockerfile runtime USER is %s, want %s:%s", img.UserID, NonRootUID, NonRootUID)
	}
	if img.StopSignal != "SIGTERM" {
		t.Errorf("I4: Dockerfile STOPSIGNAL is %s, want SIGTERM", img.StopSignal)
	}
	if img.DataChownUID != NonRootUID {
		t.Errorf("I4: Dockerfile chowns %s to UID %s, want %s", img.DataDir, img.DataChownUID, NonRootUID)
	}
	for _, id := range img.CopyChownIDs {
		if id != NonRootUID+":"+NonRootUID {
			t.Errorf("I4: a runtime COPY uses --chown %s, want %s:%s", id, NonRootUID, NonRootUID)
		}
	}
	if comp.User != "" && comp.User != img.UserID {
		t.Errorf("I4: docker-compose.yml overrides user with %q, contradicting the image USER %s", comp.User, img.UserID)
	}
}

// TestComposeEnvOwnership is invariant I5: compose may repeat an image
// environment default with the same value (reported as a duplicate) but a
// different value is drift.
func TestComposeEnvOwnership(t *testing.T) {
	img, comp, _ := mustReadAll(t, repoRoot(t))

	imageEnv := map[string]string{
		"TKT_DB_PATH": img.DBPath,
		"TKT_LISTEN":  img.Listen,
	}
	for key, got := range comp.Env {
		want, owned := imageEnv[key]
		if !owned {
			continue // a compose-only variable is compose's to declare
		}
		if got != want {
			t.Errorf("I5: docker-compose.yml sets %s=%q but the Dockerfile sets %s=%q", key, got, key, want)
			continue
		}
		t.Logf("I5: duplicate %s=%s declared identically in both files", key, got)
	}
}

// TestImageNameContract is invariant I6: the compose image repository is the
// GHCR path derived from the go.mod module path, and a tag is present. The tag
// itself is the maintainer's to choose; pinning a released tag in compose is a
// legitimate, deliberate choice and not drift (tag strategy is deferred to a
// follow-up issue).
func TestImageNameContract(t *testing.T) {
	root := repoRoot(t)
	_, comp, _ := mustReadAll(t, root)

	module, err := ModulePath(root)
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	repo, found := strings.CutPrefix(module, "github.com/")
	if !found {
		t.Fatalf("I6: module path %q is not github.com/OWNER/REPO: the GHCR derivation cannot proceed", module)
	}
	want := "ghcr.io/" + repo
	if comp.ImageRepo != want {
		t.Errorf("I6: docker-compose.yml image repository is %q, want %q (derived from go.mod %q)", comp.ImageRepo, want, module)
	}
}

// TestWorkflowSmokeContract is invariant I7: the CI smoke test feeds the image
// the same env defaults, mounts the same data directory, publishes the same
// container port and runs the same binary healthcheck. This test reads the
// workflow; it must never be the reason the workflow is edited — a
// disagreement is reported, not papered over.
func TestWorkflowSmokeContract(t *testing.T) {
	img, _, wf := mustReadAll(t, repoRoot(t))

	if wf.DBPath != img.DBPath {
		t.Errorf("I7: .github/workflows/container.yml passes TKT_DB_PATH=%q but the Dockerfile declares %q", wf.DBPath, img.DBPath)
	}
	if wf.Listen != img.Listen {
		t.Errorf("I7: .github/workflows/container.yml passes TKT_LISTEN=%q but the Dockerfile declares %q", wf.Listen, img.Listen)
	}
	if wf.VolumeTarget != img.DataDir {
		t.Errorf("I7: .github/workflows/container.yml mounts its volume at %q but the Dockerfile prepared %q", wf.VolumeTarget, img.DataDir)
	}
	_, listenPort, found := strings.Cut(img.Listen, ":")
	if !found || listenPort == "" {
		t.Fatalf("I7: Dockerfile TKT_LISTEN %q has no readable port", img.Listen)
	}
	if wf.PublishPort != listenPort {
		t.Errorf("I7: .github/workflows/container.yml publishes container port %q but the image listens on %q (TKT_LISTEN %q)", wf.PublishPort, listenPort, img.Listen)
	}
	if _, err := SelectExecHealthcheck(img, wf); err != nil {
		t.Errorf("I7: %v", err)
	}
}

// TestReleaseImageRepository is invariant I8: the release workflow's image
// repository is the lowercased GHCR path derived from the go.mod module path.
// The required ${GITHUB_REPOSITORY,,} derivation satisfies the rule by
// construction; every literal ghcr.io/<repository> spelling found in the file
// must equal the derived path, so a hardcoded ghcr.io/<other>/<other> (or an
// uppercase spelling) fails.
func TestReleaseImageRepository(t *testing.T) {
	root := repoRoot(t)
	rel := mustReadRelease(t, root)

	module, err := ModulePath(root)
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	repo, found := strings.CutPrefix(module, "github.com/")
	if !found {
		t.Fatalf("I8: module path %q is not github.com/OWNER/REPO: the GHCR derivation cannot proceed", module)
	}
	want := strings.ToLower(repo)
	if !rel.ImageRefDerived {
		t.Errorf("I8: release workflow does not derive the image reference as ghcr.io/${GITHUB_REPOSITORY,,}; the repository must never be hardcoded")
	}
	for _, got := range rel.LiteralImageRepos {
		if got != want {
			t.Errorf("I8: .github/workflows/release-container.yml hardcodes image repository %q, want %q (derived from go.mod %q)", got, want, module)
		}
	}
}

// TestReleaseBuildArgs is invariant I9: the release build must pass
// --build-arg VERSION= and --build-arg REVISION=, because the OCI labels are
// inert without them and every release would otherwise publish dev defaults.
func TestReleaseBuildArgs(t *testing.T) {
	rel := mustReadRelease(t, repoRoot(t))

	if !rel.HasVersionArg || !rel.HasRevisionArg {
		t.Errorf("I9: release workflow build args = VERSION:%v REVISION:%v; the OCI labels are inert without --build-arg VERSION= and --build-arg REVISION=", rel.HasVersionArg, rel.HasRevisionArg)
	}
}

// TestReleaseSmokeContract is invariant I10: the release smoke test agrees
// with the image contract exactly as the CI smoke test must. --env flags are
// optional there by design (the release run proves the image's own defaults),
// but any that are present must match the image defaults; the volume target
// and the published container port are required and must match, and the
// binary healthcheck must be exercised.
func TestReleaseSmokeContract(t *testing.T) {
	img, _, _ := mustReadAll(t, repoRoot(t))
	rel := mustReadRelease(t, repoRoot(t))

	if rel.Smoke.DBPath != "" && rel.Smoke.DBPath != img.DBPath {
		t.Errorf("I10: the release smoke test passes TKT_DB_PATH=%q but the Dockerfile declares %q", rel.Smoke.DBPath, img.DBPath)
	}
	if rel.Smoke.Listen != "" && rel.Smoke.Listen != img.Listen {
		t.Errorf("I10: the release smoke test passes TKT_LISTEN=%q but the Dockerfile declares %q", rel.Smoke.Listen, img.Listen)
	}
	if rel.Smoke.VolumeTarget != img.DataDir {
		t.Errorf("I10: the release smoke test mounts its volume at %q but the Dockerfile prepared %q", rel.Smoke.VolumeTarget, img.DataDir)
	}
	_, listenPort, found := strings.Cut(img.Listen, ":")
	if !found || listenPort == "" {
		t.Fatalf("I10: Dockerfile TKT_LISTEN %q has no readable port", img.Listen)
	}
	if rel.Smoke.PublishPort != listenPort {
		t.Errorf("I10: the release smoke test publishes container port %q but the image listens on %q (TKT_LISTEN %q)", rel.Smoke.PublishPort, listenPort, img.Listen)
	}
	if _, err := SelectExecHealthcheck(img, rel.Smoke); err != nil {
		t.Errorf("I10: %v", err)
	}
}

// TestParseReleaseWorkflowRejectsMissing triangulates the release extractor's
// non-vacuity: a workflow the extractors cannot read must fail, never produce
// a contract that quietly checks nothing.
func TestParseReleaseWorkflowRejectsMissing(t *testing.T) {
	head := "jobs:\n  publish:\n    run: |\n"
	derived := `          IMAGE="ghcr.io/${GITHUB_REPOSITORY,,}"` + "\n"
	args := "          docker build --build-arg VERSION=\"$VERSION\" --build-arg REVISION=\"$GITHUB_SHA\" --tag \"$IMAGE:$VERSION\"\n"
	run := "          docker run --volume \"$V:/data\" --publish 127.0.0.1:80:8080 img\n          docker exec \"$C\" /server -healthcheck\n"

	noDerived := head + args + run
	if _, err := parseReleaseWorkflow([]byte(noDerived)); err == nil || !strings.Contains(err.Error(), "ghcr.io/${GITHUB_REPOSITORY,,}") {
		t.Errorf("expected a missing derived-image-reference error, got %v", err)
	}

	noArgs := head + derived + run
	if _, err := parseReleaseWorkflow([]byte(noArgs)); err == nil || !strings.Contains(err.Error(), "--build-arg") {
		t.Errorf("expected a missing build-arg error, got %v", err)
	}

	noSmoke := head + derived + args
	if _, err := parseReleaseWorkflow([]byte(noSmoke)); err == nil || !strings.Contains(err.Error(), "--volume") {
		t.Errorf("expected a missing smoke-flag error, got %v", err)
	}
}

// dockerfileWithRuntimeContract returns a minimal but complete Dockerfile so
// regression tests can vary one part without losing the required fields.
func dockerfileWithRuntimeContract(runtime string) []byte {
	return []byte(`FROM golang:1.25.14 AS build
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=65532:65532 /out/server /server
` + runtime)
}

const fullRuntimeStage = `ENV TKT_DB_PATH=/data/tkt.db
ENV TKT_LISTEN=:8080
EXPOSE 8080
USER 65532:65532
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/server","-healthcheck"]
ENTRYPOINT ["/server"]
`

// TestParseDockerfileIgnoresBuildStageContractValues is the stage-scoping
// regression: the runtime stage is everything after the LAST FROM, so
// build-stage USER, COPY --chown, EXPOSE and ENV TKT_* are invisible to the
// contract, while the build-stage prepared data directory is still read.
func TestParseDockerfileIgnoresBuildStageContractValues(t *testing.T) {
	body := []byte(`FROM golang:1.25.14 AS build
USER 1111:1111
COPY --chown=0:0 go.mod go.sum ./
ENV TKT_DB_PATH=/data/wrong.db
ENV TKT_LISTEN=:9999
EXPOSE 9999
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=65532:65532 /out/server /server
RUN mkdir -p /other && chown 0:0 /other
` + fullRuntimeStage)

	c, err := parseDockerfile(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.UserID != NonRootUID+":"+NonRootUID {
		t.Errorf("build-stage USER 1111:1111 leaked into the contract: runtime USER = %s", c.UserID)
	}
	for _, id := range c.CopyChownIDs {
		if id != NonRootUID+":"+NonRootUID {
			t.Errorf("build-stage COPY --chown %s leaked into the contract", id)
		}
	}
	if c.DBPath != "/data/tkt.db" || c.Listen != ":8080" {
		t.Errorf("build-stage ENV leaked into the contract: TKT_DB_PATH=%q TKT_LISTEN=%q", c.DBPath, c.Listen)
	}
	if c.ExposePort != "8080" {
		t.Errorf("build-stage EXPOSE 9999 leaked into the contract: EXPOSE = %s", c.ExposePort)
	}
	if c.DataDir != "/data" || c.DataChownUID != NonRootUID {
		t.Errorf("the build-stage prepared directory must still be read: got %s (chown %s)", c.DataDir, c.DataChownUID)
	}
}

// TestParseDockerfileToleratesEquivalentForms is the tolerance rule:
// equivalent spellings of the same contract parse instead of failing as
// unreadable, while shell-form HEALTHCHECK and ENTRYPOINT still fail loudly
// with the required form named.
func TestParseDockerfileToleratesEquivalentForms(t *testing.T) {
	body := dockerfileWithRuntimeContract(`ENV TKT_DB_PATH=/data/tkt.db
ENV TKT_LISTEN=:8080
EXPOSE 8080/tcp
USER 65532
STOPSIGNAL SIGTERM
HEALTHCHECK CMD ["/server","-healthcheck"]
ENTRYPOINT ["/server"]
`)
	c, err := parseDockerfile(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.ExposePort != "8080" {
		t.Errorf("EXPOSE 8080/tcp: got port %q, want 8080 with the protocol dropped", c.ExposePort)
	}
	if c.UserID != NonRootUID+":"+NonRootUID {
		t.Errorf("USER 65532: got %q, want %s:%s with the gid defaulted to the uid", c.UserID, NonRootUID, NonRootUID)
	}

	shell := dockerfileWithRuntimeContract(`ENV TKT_DB_PATH=/data/tkt.db
ENV TKT_LISTEN=:8080
EXPOSE 8080
USER 65532:65532
STOPSIGNAL SIGTERM
HEALTHCHECK CMD /server -healthcheck
ENTRYPOINT /server
`)
	_, err = parseDockerfile(shell)
	if err == nil {
		t.Fatal("expected a shell-form HEALTHCHECK/ENTRYPOINT error, got nil")
	}
	for _, want := range []string{"HEALTHCHECK", "ENTRYPOINT", "JSON exec form"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestParseDockerfileEntrypointMatchesHealthcheck is the G5 rule: the runtime
// ENTRYPOINT must invoke the same binary as the HEALTHCHECK, because compose
// cannot override either and a split would run a different binary than the
// one being health-checked.
func TestParseDockerfileEntrypointMatchesHealthcheck(t *testing.T) {
	body := dockerfileWithRuntimeContract(`ENV TKT_DB_PATH=/data/tkt.db
ENV TKT_LISTEN=:8080
EXPOSE 8080
USER 65532:65532
STOPSIGNAL SIGTERM
HEALTHCHECK CMD ["/server","-healthcheck"]
ENTRYPOINT ["/init"]
`)
	_, err := parseDockerfile(body)
	if err == nil || !strings.Contains(err.Error(), "/init") || !strings.Contains(err.Error(), "/server") {
		t.Errorf("expected an ENTRYPOINT/HEALTHCHECK binary mismatch error naming both, got %v", err)
	}
}

// TestParseDockerfileRecordsChownGid is the G5 rule: the uid AND the gid of
// every runtime COPY --chown are captured, so a --chown=65532:0 cannot hide
// behind a correct uid.
func TestParseDockerfileRecordsChownGid(t *testing.T) {
	body := dockerfileWithRuntimeContract(`COPY --from=build --chown=65532:0 /out/server /server
` + fullRuntimeStage)

	c, err := parseDockerfile(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var found65532First, foundMixedGid bool
	for _, id := range c.CopyChownIDs {
		if id == NonRootUID+":"+NonRootUID {
			found65532First = true
		}
		if id == "65532:0" {
			foundMixedGid = true
		}
	}
	if !found65532First || !foundMixedGid {
		t.Errorf("COPY --chown ids = %v, want both 65532:65532 and 65532:0: the gid must be recorded", c.CopyChownIDs)
	}
}

// TestParseDockerfileRejectsIncomplete triangulates the non-vacuity rule: a
// Dockerfile the extractors cannot read must fail, never produce a contract
// that quietly checks nothing.
func TestParseDockerfileRejectsIncomplete(t *testing.T) {
	_, err := parseDockerfile([]byte("FROM scratch\nEXPOSE 8080\nUSER 65532:65532\n"))
	if err == nil {
		t.Fatal("expected an error for a Dockerfile without ENV/HEALTHCHECK/mkdir lines, got nil")
	}
	for _, want := range []string{"TKT_DB_PATH", "TKT_LISTEN", "data directory", "HEALTHCHECK", "ENTRYPOINT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the missing %s", err, want)
		}
	}
}

// composeBody returns a minimal valid compose file so regression tests can
// vary the service body without losing the required fields.
func composeBody(service string) []byte {
	return []byte(`services:
  app:
    image: ghcr.io/giulianotesta7/tkt:${TKT_TAG:-latest}
` + service)
}

const composeTail = `    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "8080:8080"
    volumes:
      - tkt-data:/data
`

// TestParseComposeReadsListFormEnvironment is the F2 rule: the list spelling
// of environment (- KEY=value) must land in Env so I5 covers it, instead of
// falling through the list-item branch unnoticed.
func TestParseComposeReadsListFormEnvironment(t *testing.T) {
	body := composeBody(composeTail + `    environment:
      - TKT_DB_PATH=/data/other.db
      - TKT_LISTEN=:9090
`)
	c, err := parseCompose(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Env["TKT_DB_PATH"] != "/data/other.db" || c.Env["TKT_LISTEN"] != ":9090" {
		t.Errorf("list-form environment not read into Env: %v", c.Env)
	}

	unreadable := composeBody(composeTail + `    environment:
      - TKT_DB_PATH
`)
	if _, err := parseCompose(unreadable); err == nil || !strings.Contains(err.Error(), "environment entry") {
		t.Errorf("expected an unreadable-environment-entry error, got %v", err)
	}
}

// TestParseComposeUnquotesScalarValues is the G4 rule: every compose scalar
// the check compares loses exactly one layer of matching quotes — image,
// ports (single or double), volumes, environment keys and values, user — so a
// valid YAML spelling is not reported as drift.
func TestParseComposeUnquotesScalarValues(t *testing.T) {
	body := []byte(`services:
  app:
    image: "ghcr.io/giulianotesta7/tkt:${TKT_TAG:-latest}"
    build:
      context: '.'
      dockerfile: "Dockerfile"
    ports:
      - '8080:8080'
    volumes:
      - "tkt-data:/data"
    environment:
      "TKT_DB_PATH": '/data/tkt.db'
    user: '65532:65532'
`)
	c, err := parseCompose(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.ImageRepo != "ghcr.io/giulianotesta7/tkt" || c.ImageTag != "${TKT_TAG:-latest}" {
		t.Errorf("quoted image kept its quotes: %q:%q", c.ImageRepo, c.ImageTag)
	}
	if c.BuildContext != "." || c.BuildDockerfile != "Dockerfile" {
		t.Errorf("quoted build block kept its quotes: context %q dockerfile %q", c.BuildContext, c.BuildDockerfile)
	}
	if c.HostPort != "8080" || c.ContainerPort != "8080" {
		t.Errorf("single-quoted port kept its quotes: %q->%q", c.HostPort, c.ContainerPort)
	}
	if c.VolumeName != "tkt-data" || c.VolumeTarget != "/data" {
		t.Errorf("quoted volume kept its quotes: %q at %q", c.VolumeName, c.VolumeTarget)
	}
	if c.Env["TKT_DB_PATH"] != "/data/tkt.db" {
		t.Errorf("quoted environment key or value kept its quotes: %q", c.Env["TKT_DB_PATH"])
	}
	if c.User != "65532:65532" {
		t.Errorf("single-quoted user kept its quotes: user = %q", c.User)
	}
}

// TestParseComposeBuildContext is the G1 rule: the build sub-block is parsed,
// context must be "." and dockerfile, when present, must be "Dockerfile", so
// compose cannot point the check at one Dockerfile and run another.
func TestParseComposeBuildContext(t *testing.T) {
	c, err := parseCompose(composeBody(composeTail))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.BuildContext != "." || c.BuildDockerfile != "Dockerfile" {
		t.Errorf("build block = context %q dockerfile %q, want . and Dockerfile", c.BuildContext, c.BuildDockerfile)
	}

	inline, err := parseCompose(composeBody(`    build: .
    ports:
      - "8080:8080"
    volumes:
      - tkt-data:/data
`))
	if err != nil || inline.BuildContext != "." || inline.BuildDockerfile != "" {
		t.Errorf("inline build context: got %v, err %v", inline.BuildContext, err)
	}

	// H2: one leading ./ is an equivalent spelling of the same target.
	slash, err := parseCompose(composeBody(`    build:
      context: ./
      dockerfile: ./Dockerfile
    ports:
      - "8080:8080"
    volumes:
      - tkt-data:/data
`))
	if err != nil {
		t.Fatalf("./ spellings of the same target must parse, got %v", err)
	}
	if slash.BuildContext != "./" || slash.BuildDockerfile != "./Dockerfile" {
		t.Errorf("./ spellings mangled: context %q dockerfile %q", slash.BuildContext, slash.BuildDockerfile)
	}

	otherContext := composeBody(`    build:
      context: /srv/other
    ports:
      - "8080:8080"
    volumes:
      - tkt-data:/data
`)
	if _, err := parseCompose(otherContext); err == nil || !strings.Contains(err.Error(), "/srv/other") {
		t.Errorf("expected a build-context error naming /srv/other, got %v", err)
	}

	otherDockerfile := composeBody(composeTail)
	otherDockerfile = []byte(strings.Replace(string(otherDockerfile), "dockerfile: Dockerfile", "dockerfile: other.Dockerfile", 1))
	if _, err := parseCompose(otherDockerfile); err == nil || !strings.Contains(err.Error(), "other.Dockerfile") {
		t.Errorf("expected a build.dockerfile error naming other.Dockerfile, got %v", err)
	}

	extraBuildKey := composeBody(`    build:
      context: .
      args:
        FOO: bar
    ports:
      - "8080:8080"
    volumes:
      - tkt-data:/data
`)
	if _, err := parseCompose(extraBuildKey); err == nil || !strings.Contains(err.Error(), "args") {
		t.Errorf("expected an unallowed build key error naming args, got %v", err)
	}
}

// TestParseComposeRejectsUnlistedServiceKeys is the G1 rule: service keys are
// allowlisted by name. Every key outside the allowlist fails loudly and names
// itself — including the former deny list (command, entrypoint, env_file) and
// the higher-impact keys (stop_signal, privileged, cap_add, security_opt).
// The three benign keys stay parsed-or-ignored without error.
func TestParseComposeRejectsUnlistedServiceKeys(t *testing.T) {
	for _, key := range []string{"command", "entrypoint", "env_file", "stop_signal", "privileged", "cap_add", "security_opt", "container_name"} {
		bypassing := composeBody(composeTail + fmt.Sprintf("    %s: something\n", key))
		_, err := parseCompose(bypassing)
		if err == nil {
			t.Errorf("key %q: expected an unlisted-service-key error, got nil", key)
			continue
		}
		for _, want := range []string{key, "allowlist", "allow it deliberately"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("key %q: error %q does not mention %q", key, err, want)
			}
		}
	}

	benign := composeBody(composeTail + `    restart: unless-stopped
    depends_on:
      - other
    networks:
      - front
`)
	c, err := parseCompose(benign)
	if err != nil {
		t.Fatalf("benign allowlisted keys must stay accepted, got %v", err)
	}
	if c.ContainerPort != "8080" || c.VolumeTarget != "/data" {
		t.Errorf("benign-key parse lost required values: %+v", c)
	}
}

// TestParseComposeRejectsHealthcheckSubKeys is the H1 rule: the healthcheck
// sub-block allowlists test as its only sub-key — the image owns the
// healthcheck, so compose must not retune (interval, timeout, retries,
// start_period) or disable it. A restated test is still parsed as today.
func TestParseComposeRejectsHealthcheckSubKeys(t *testing.T) {
	for _, key := range []string{"disable", "interval", "timeout", "retries", "start_period"} {
		body := composeBody(composeTail + fmt.Sprintf("    healthcheck:\n      test: [\"CMD\", \"/server\", \"-healthcheck\"]\n      %s: something\n", key))
		_, err := parseCompose(body)
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "image owns the healthcheck") {
			t.Errorf("healthcheck key %q: expected a rejection naming the key and the image ownership, got %v", key, err)
		}
	}

	restated := composeBody(composeTail + `    healthcheck:
      test: ["CMD", "/server", "-healthcheck"]
`)
	c, err := parseCompose(restated)
	if err != nil || !c.HasHealthcheck || c.HealthcheckTest == "" {
		t.Errorf("a restated identical test must still parse: %+v, err %v", c, err)
	}
}

// TestParseComposeRejectsUnreadableValues triangulates the fail-loudly rule:
// a volume line without an absolute target is a parse error, not a skip.
func TestParseComposeRejectsUnreadableValues(t *testing.T) {
	body := []byte(`services:
  app:
    image: ghcr.io/giulianotesta7/tkt:${TKT_TAG:-latest}
    ports:
      - "8080:8080"
    volumes:
      - tkt-data
`)
	_, err := parseCompose(body)
	if err == nil {
		t.Fatal("expected an error for an unreadable volume entry, got nil")
	}
	if !strings.Contains(err.Error(), "volume") {
		t.Errorf("error %q does not name the unreadable field", err)
	}
}

// TestParseComposeRejectsSecondService triangulates the one-service shape.
func TestParseComposeRejectsSecondService(t *testing.T) {
	body := []byte(`services:
  app:
    image: ghcr.io/giulianotesta7/tkt:${TKT_TAG:-latest}
    ports:
      - "8080:8080"
    volumes:
      - tkt-data:/data
  sidecar:
    image: busybox
    ports:
      - "9000:9000"
    volumes:
      - tkt-data:/data
`)
	if _, err := parseCompose(body); err == nil || !strings.Contains(err.Error(), "second service") {
		t.Errorf("expected a second-service error, got %v", err)
	}
}

// TestParseWorkflowCoversShortFlagsAndExec is the F4 rule: the workflow scan
// must catch a short -v with a wrong target, a publish mapping whose
// container port disagrees, and must collect the docker exec invocations.
func TestParseWorkflowCoversShortFlagsAndExec(t *testing.T) {
	good := "steps:\n  - run: docker run --env TKT_DB_PATH=/data/tkt.db --env TKT_LISTEN=:8080 --volume v:/data --publish 127.0.0.1:80:8080 img\n  - run: docker exec ctr /server -healthcheck\n"
	c, err := parseWorkflow([]byte(good))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.VolumeTarget != "/data" || c.PublishPort != "8080" {
		t.Errorf("workflow contract extracted wrong: %+v", c)
	}
	if len(c.ExecInvocations) != 1 || c.ExecInvocations[0] != [2]string{"/server", "-healthcheck"} {
		t.Errorf("exec invocations = %v, want [/server -healthcheck]", c.ExecInvocations)
	}

	// A short -v with a wrong target must fail even though a long --volume
	// with the right target exists in the same file.
	wrongVolume := "steps:\n  - run: docker run --env TKT_DB_PATH=/x --env TKT_LISTEN=:1 --volume v:/data -p 80:1 img\n  - run: docker run -v \"$VOLUME:/data-wrong\" img\n  - run: docker exec ctr /server -healthcheck\n"
	if _, err := parseWorkflow([]byte(wrongVolume)); err == nil || !strings.Contains(err.Error(), "/data-wrong") {
		t.Errorf("expected a two-targets error naming /data-wrong, got %v", err)
	}

	// Two publish mappings with different container ports must fail.
	wrongPort := "steps:\n  - run: docker run --env TKT_DB_PATH=/x --env TKT_LISTEN=:1 --volume v:/data --publish 127.0.0.1:80:8080 img\n  - run: docker run -p 81:9090 img\n  - run: docker exec ctr /server -healthcheck\n"
	if _, err := parseWorkflow([]byte(wrongPort)); err == nil || !strings.Contains(err.Error(), "9090") {
		t.Errorf("expected a two-published-ports error naming 9090, got %v", err)
	}

	// A file without any docker exec invocation must fail loudly.
	noExec := "steps:\n  - run: docker run --env TKT_DB_PATH=/x --env TKT_LISTEN=:1 --volume v:/data -p 80:1 img\n"
	if _, err := parseWorkflow([]byte(noExec)); err == nil || !strings.Contains(err.Error(), "docker exec") {
		t.Errorf("expected a missing docker exec error, got %v", err)
	}
}

// TestParseWorkflowToleratesBenignExec is the G3 regression: a benign
// diagnostic exec next to the real healthcheck must not fail the parse —
// selection by binary is SelectExecHealthcheck's job, not the scanner's.
func TestParseWorkflowToleratesBenignExec(t *testing.T) {
	body := "steps:\n  - run: docker run --env TKT_DB_PATH=/x --env TKT_LISTEN=:1 --volume v:/data -p 80:1 img\n  - run: docker exec \"$C\" ls -l /data || true\n  - run: docker exec \"$C\" /server -healthcheck\n"
	c, err := parseWorkflow([]byte(body))
	if err != nil {
		t.Fatalf("benign exec must not fail the parse, got %v", err)
	}
	if len(c.ExecInvocations) != 2 {
		t.Errorf("exec invocations = %v, want both collected", c.ExecInvocations)
	}
}

// TestSelectExecHealthcheck is the G3 selection rule: only invocations running
// the image's healthcheck binary are selected (others ignored by name), at
// least one must match, and its argument must equal the image HEALTHCHECK's.
func TestSelectExecHealthcheck(t *testing.T) {
	img := ImageContract{HealthcheckBin: "/server", HealthcheckArg: "-healthcheck"}

	wf := WorkflowContract{ExecInvocations: [][2]string{{"ls", "-l"}, {"/server", "-healthcheck"}}}
	if sel, err := SelectExecHealthcheck(img, wf); err != nil || sel != [2]string{"/server", "-healthcheck"} {
		t.Errorf("benign diagnostics must be ignored by name: got %v, err %v", sel, err)
	}

	wf = WorkflowContract{ExecInvocations: [][2]string{{"ls", "-l"}}}
	if _, err := SelectExecHealthcheck(img, wf); err == nil || !strings.Contains(err.Error(), "/server") {
		t.Errorf("expected a no-match error naming /server, got %v", err)
	}

	wf = WorkflowContract{ExecInvocations: [][2]string{{"/server", "-wrong"}}}
	if _, err := SelectExecHealthcheck(img, wf); err == nil || !strings.Contains(err.Error(), "-wrong") {
		t.Errorf("expected an argument disagreement error naming -wrong, got %v", err)
	}
}

// TestParseWorkflowRejectsMissingEnvs triangulates the workflow extractor.
func TestParseWorkflowRejectsMissingEnvs(t *testing.T) {
	if _, err := parseWorkflow([]byte("steps:\n  - run: docker run --volume v:/data image\n")); err == nil || !strings.Contains(err.Error(), "--env") {
		t.Errorf("expected a missing --env error, got %v", err)
	}
	if _, err := parseWorkflow([]byte("steps:\n  - run: docker run --env TKT_DB_PATH=/data/tkt.db --env TKT_LISTEN=:8080 image\n")); err == nil || !strings.Contains(err.Error(), "--volume") {
		t.Errorf("expected a missing --volume error, got %v", err)
	}
}
