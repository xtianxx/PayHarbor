// Package baseline holds contract tests for the A3/A4 baseline tooling.
//
// This file pins the CI wiring of .github/workflows/regression.yml: the native
// job must build the container image from the current checkout, run the Go
// suite against a dedicated PostgreSQL through scripts/baseline/run-tests.sh,
// and make the fail-closed gate the single required conclusion. The fallback
// job stays supplementary and can never mask the native result.
package baseline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflowStep struct {
	Name            string         `yaml:"name"`
	ID              string         `yaml:"id"`
	If              string         `yaml:"if"`
	Uses            string         `yaml:"uses"`
	Run             string         `yaml:"run"`
	ContinueOnError any            `yaml:"continue-on-error"`
	Env             map[string]any `yaml:"env"`
	With            map[string]any `yaml:"with"`
}

type workflowJob struct {
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Env            map[string]string `yaml:"env"`
	Steps          []workflowStep    `yaml:"steps"`
}

type workflowFile struct {
	// Concurrency is deliberately untyped at the workflow level: the contract
	// test accepts only the mapping form with an explicit cancel flag.
	Concurrency any                    `yaml:"concurrency"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

// Official Docker actions, pinned to the commit SHAs behind their current
// major tags (verified via the GitHub API against docker/setup-buildx-action
// v4 and docker/build-push-action v7).
const (
	officialSetupBuildx = "docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069"
	officialBuildPush   = "docker/build-push-action@c3c9e263c25d99ce0380d002d59b67737d91b0dc"
	// ghaCacheScope is the shared BuildKit cache namespace for the native
	// linux/amd64 gotest image. Only trusted main runs (push or
	// workflow_dispatch) may write it.
	ghaCacheScope = "payharbor-native-linux-amd64-v1"
)

func loadWorkflow(t *testing.T) (workflowFile, string) {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "workflows", "regression.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var parsed workflowFile
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse workflow YAML: %v", err)
	}
	return parsed, string(raw)
}

func stepByID(steps []workflowStep, id string) (workflowStep, bool) {
	for _, step := range steps {
		if step.ID == id {
			return step, true
		}
	}
	return workflowStep{}, false
}

func stepsRunning(steps []workflowStep, needle string) []workflowStep {
	var out []workflowStep
	for _, step := range steps {
		if strings.Contains(step.Run, needle) {
			out = append(out, step)
		}
	}
	return out
}

func continueOnError(step workflowStep) bool {
	switch value := step.ContinueOnError.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true")
	default:
		return false
	}
}

func requireJob(t *testing.T, wf workflowFile, name string) workflowJob {
	t.Helper()
	job, ok := wf.Jobs[name]
	if !ok {
		t.Fatalf("workflow job %q is missing", name)
	}
	return job
}

func stepsUsing(steps []workflowStep, prefix string) []workflowStep {
	var out []workflowStep
	for _, step := range steps {
		if strings.HasPrefix(step.Uses, prefix) {
			out = append(out, step)
		}
	}
	return out
}

func cloneStep(step workflowStep) workflowStep {
	out := step
	out.Env = make(map[string]any, len(step.Env))
	for key, value := range step.Env {
		out.Env[key] = value
	}
	out.With = make(map[string]any, len(step.With))
	for key, value := range step.With {
		out.With[key] = value
	}
	return out
}

func stringWith(step workflowStep, key string) string {
	value, _ := step.With[key].(string)
	return value
}

// approvedCacheTo is the only accepted cache export expression: trusted main
// runs (push or workflow_dispatch) export the shared scope with mode=max; every
// other run, including pull_request, evaluates to the empty string. Exact
// matching (not substring checks) keeps a rewritten expression from passing
// while actually exporting from untrusted refs.
func approvedCacheTo() string {
	return "${{ github.ref == 'refs/heads/main' && github.event_name != 'pull_request' && 'type=gha,mode=max,scope=" + ghaCacheScope + "' || '' }}"
}

// validateCacheInputs accepts only a shared-scope GHA cache that is read on
// every event and written by the single approved expression: PRs and forks
// may read the main scope but must never poison it, and a missing cache or an
// unconditional/wrong-branch write would either slow every run or let
// untrusted code own the cache.
func validateCacheInputs(step workflowStep) error {
	cacheFrom := stringWith(step, "cache-from")
	if cacheFrom != "type=gha,scope="+ghaCacheScope {
		return fmt.Errorf("cache-from = %q, want type=gha,scope=%s", cacheFrom, ghaCacheScope)
	}
	cacheTo := stringWith(step, "cache-to")
	if cacheTo != approvedCacheTo() {
		return fmt.Errorf("cache-to = %q, want exactly %q", cacheTo, approvedCacheTo())
	}
	return nil
}

// validateNativeImageStep enforces the single official image build: the
// current checkout is the only context, the built image is loaded into the
// job's Docker daemon for the compose stack, nothing is pushed to a registry,
// and the action's own build-record upload is disabled so the explicit
// evidence whitelist stays the only artifact producer.
func validateNativeImageStep(step workflowStep) error {
	if step.Uses != officialBuildPush {
		return fmt.Errorf("image build must use %s, got %q", officialBuildPush, step.Uses)
	}
	if step.Run != "" {
		return fmt.Errorf("image build must not shell out to docker build, got %q", step.Run)
	}
	if got := stringWith(step, "context"); got != "." {
		return fmt.Errorf("build context = %q, want the current checkout %q", got, ".")
	}
	if got := stringWith(step, "file"); got != "./Dockerfile" {
		return fmt.Errorf("build file = %q, want the current checkout's ./Dockerfile", got)
	}
	if got := stringWith(step, "target"); got != "gotest" {
		return fmt.Errorf("build target = %q, want gotest", got)
	}
	if got := stringWith(step, "platforms"); got != "linux/amd64" {
		return fmt.Errorf("build platforms = %q, want linux/amd64", got)
	}
	if load, ok := step.With["load"].(bool); !ok || !load {
		return fmt.Errorf("build load = %v, want true (compose must use this image)", step.With["load"])
	}
	if push, ok := step.With["push"].(bool); !ok || push {
		return fmt.Errorf("build push = %v, want false (no registry publish)", step.With["push"])
	}
	if got := stringWith(step, "tags"); got != "${{ env.P0_TEST_IMAGE }}" {
		return fmt.Errorf("build tags = %q, want ${{ env.P0_TEST_IMAGE }}", got)
	}
	if got := stringWith(step, "build-args"); got != "BUILD_JOBS=${{ env.BUILD_JOBS }}" {
		return fmt.Errorf("build-args = %q, want BUILD_JOBS=${{ env.BUILD_JOBS }}", got)
	}
	if err := validateCacheInputs(step); err != nil {
		return err
	}
	if record, _ := step.Env["DOCKER_BUILD_RECORD_UPLOAD"].(string); record != "false" {
		return fmt.Errorf("DOCKER_BUILD_RECORD_UPLOAD = %q, want \"false\" (no implicit build artifact)", record)
	}
	blob := fmt.Sprint(step.With)
	for _, forbidden := range []string{
		"no-cache", "cacheonly", "--cache-only", "ghcr.io", "docker.io/",
		"walletcorefallback",
	} {
		if strings.Contains(blob, forbidden) {
			return fmt.Errorf("image build must not reference %q", forbidden)
		}
	}
	return nil
}

// validateConcurrency requires workflow-level supersession keyed by workflow
// and ref: a newer run for the same ref replaces the queued/running one,
// while other branches, PRs and workflows keep independent groups. Run IDs or
// commit SHAs in the group would make every run unique and disable the
// supersession.
func validateConcurrency(value any) error {
	settings, ok := value.(map[string]any)
	if !ok {
		if value == nil {
			return fmt.Errorf("workflow has no concurrency block; superseded runs would keep running")
		}
		return fmt.Errorf("workflow concurrency must be a mapping with group and cancel-in-progress, got %T", value)
	}
	group, _ := settings["group"].(string)
	for _, want := range []string{"${{ github.workflow }}", "${{ github.ref }}"} {
		if !strings.Contains(group, want) {
			return fmt.Errorf("concurrency group %q must include %s", group, want)
		}
	}
	for _, forbidden := range []string{"run_id", "run_attempt", "github.sha"} {
		if strings.Contains(group, forbidden) {
			return fmt.Errorf("concurrency group %q must not include %s (it would disable supersession)", group, forbidden)
		}
	}
	cancel, ok := settings["cancel-in-progress"].(bool)
	if !ok || !cancel {
		return fmt.Errorf("cancel-in-progress = %v, want true", settings["cancel-in-progress"])
	}
	return nil
}

func mappingValue(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1], true
		}
	}
	return nil, false
}

func TestNativeJobShapeAndFallbackSeparation(t *testing.T) {
	wf, _ := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")
	fallback := requireJob(t, wf, "fallback-regression")

	if native.Env["BUILD_JOBS"] != "2" {
		t.Fatalf("native BUILD_JOBS = %q, want \"2\" (CI memory headroom)", native.Env["BUILD_JOBS"])
	}
	if native.TimeoutMinutes <= 0 || native.TimeoutMinutes > 180 {
		t.Fatalf("native timeout-minutes = %d, want 1..180", native.TimeoutMinutes)
	}
	var checkoutFound bool
	for _, step := range native.Steps {
		if strings.Contains(step.Uses, "actions/checkout@") {
			checkoutFound = true
			if got, _ := step.With["submodules"].(string); got != "recursive" {
				t.Fatalf("checkout submodules = %q, want recursive", got)
			}
		}
	}
	if !checkoutFound {
		t.Fatal("native job has no checkout step")
	}
	if len(stepsRunning(fallback.Steps, "GATEWAY_REGRESSION_MODE=fallback scripts/regression.sh")) != 1 {
		t.Fatal("fallback job must stay the container-free fallback regression run")
	}
	if len(stepsRunning(fallback.Steps, "verify-test-json.py")) != 0 {
		t.Fatal("fallback job must not run the native gate")
	}
}

func TestNativeJobBuildsImageAndStartsDedicatedPostgres(t *testing.T) {
	wf, _ := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	builds := stepsUsing(native.Steps, "docker/build-push-action@")
	if len(builds) != 1 {
		t.Fatalf("want exactly one docker/build-push-action build step, got %d", len(builds))
	}
	if err := validateNativeImageStep(builds[0]); err != nil {
		t.Fatalf("native image build contract: %v", err)
	}
	setups := stepsUsing(native.Steps, "docker/setup-buildx-action@")
	if len(setups) != 1 {
		t.Fatalf("want exactly one docker/setup-buildx-action step, got %d", len(setups))
	}
	if setups[0].Uses != officialSetupBuildx {
		t.Fatalf("setup-buildx uses = %q, want %q", setups[0].Uses, officialSetupBuildx)
	}
	if setups[0].Run != "" {
		t.Fatalf("setup-buildx must not use a shell run block, got %q", setups[0].Run)
	}
	if got := stepsUsing(native.Steps, "docker/login-action@"); len(got) != 0 {
		t.Fatal("native job must not log in to a container registry")
	}
	for _, step := range native.Steps {
		if strings.Contains(step.Run, "docker build") {
			t.Fatalf("native job must build only through the pinned action, found shell build: %q", step.Run)
		}
	}
	if native.Env["BUILD_JOBS"] != "2" {
		t.Fatalf("native BUILD_JOBS = %q, want \"2\" (single build-arg source)", native.Env["BUILD_JOBS"])
	}

	pgSteps := stepsRunning(native.Steps, "up -d postgres --wait")
	if len(pgSteps) != 1 {
		t.Fatalf("want exactly one dedicated PostgreSQL start step, got %d", len(pgSteps))
	}
	pg := pgSteps[0].Run
	for _, want := range []string{"deploy/compose/baseline/docker-compose.yml", "pg_isready", "--env-file \"$P0_ENV_FILE\""} {
		if !strings.Contains(pg, want) {
			t.Fatalf("postgres step missing %q", want)
		}
	}
}

func TestRunnerStepAndFailClosedGate(t *testing.T) {
	wf, raw := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	runner, ok := stepByID(native.Steps, "runner")
	if !ok {
		t.Fatal("runner step is missing")
	}
	if !continueOnError(runner) {
		t.Fatal("runner step must use continue-on-error so the gate still runs")
	}
	for _, want := range []string{"--profile test", "scripts/baseline/run-tests.sh --evidence /evidence"} {
		if !strings.Contains(runner.Run, want) {
			t.Fatalf("runner step missing %q", want)
		}
	}

	gateSteps := stepsRunning(native.Steps, "verify-test-json.py")
	if len(gateSteps) != 1 {
		t.Fatalf("want exactly one gate step, got %d", len(gateSteps))
	}
	gate := gateSteps[0]
	if !strings.Contains(gate.If, "always()") {
		t.Fatalf("gate must run with if: always(), got %q", gate.If)
	}
	for _, want := range []string{
		"--json \"$P0_EVIDENCE_DIR/test.jsonl\"",
		"--exit-code \"$P0_EVIDENCE_DIR/exit-code\"",
		"--vet-exit-code \"$P0_EVIDENCE_DIR/vet-exit-code\"",
		"--build-exit-code \"$P0_EVIDENCE_DIR/build-exit-code\"",
		"scripts/baseline/must-run.txt",
		"scripts/baseline/allow-skip.txt",
		"--summary \"$P0_EVIDENCE_DIR/summary.json\"",
	} {
		if !strings.Contains(gate.Run, want) {
			t.Fatalf("gate step missing %q", want)
		}
	}
	if strings.Contains(raw, "| tail") {
		t.Fatal("workflow must not pipe test output through tail (masks exit codes)")
	}
}

func TestEvidenceUploadIsExplicitWhitelist(t *testing.T) {
	wf, raw := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	var upload *workflowStep
	for index := range native.Steps {
		if strings.Contains(native.Steps[index].Uses, "actions/upload-artifact@") {
			upload = &native.Steps[index]
		}
	}
	if upload == nil {
		t.Fatal("evidence upload step is missing")
	}
	if !strings.Contains(upload.Uses, "actions/upload-artifact@v4") {
		t.Fatalf("upload action = %q, want actions/upload-artifact@v4", upload.Uses)
	}
	if !strings.Contains(upload.If, "always()") {
		t.Fatal("evidence upload must run with if: always()")
	}
	path, _ := upload.With["path"].(string)
	lines := strings.Split(strings.TrimSpace(path), "\n")
	wantFiles := []string{
		"go-version.txt", "build.txt", "build.stderr", "vet.txt", "vet.stderr",
		"test.jsonl", "test.stderr", "build-exit-code", "vet-exit-code",
		"exit-code", "summary.json",
	}
	if len(lines) != len(wantFiles) {
		t.Fatalf("artifact whitelist has %d entries, want %d (%q)", len(lines), len(wantFiles), path)
	}
	for index, file := range wantFiles {
		entry := strings.TrimSpace(lines[index])
		if !strings.HasSuffix(entry, "/evidence/"+file) {
			t.Fatalf("artifact whitelist entry %d = %q, want suffix /evidence/%s", index, entry, file)
		}
		if !strings.HasPrefix(entry, "${{ runner.temp }}/gateway-native-") {
			t.Fatalf("artifact whitelist entry %d is not scoped to the run dir: %q", index, entry)
		}
	}
	if strings.Contains(path, "*") {
		t.Fatalf("artifact path must not contain wildcards: %q", path)
	}
	if strings.Contains(path, "P0_ENV_FILE") || strings.Contains(path, "P0_DSN_FILE") ||
		strings.Contains(path, ".env") || strings.Contains(path, ".git") {
		t.Fatal("artifact whitelist must not reference env files or VCS data")
	}
	if strings.Contains(raw, "cat \"$P0_ENV_FILE\"") || strings.Contains(raw, "env\n") {
		t.Fatal("workflow must not dump environment files")
	}
}

func TestCleanupRunsAlwaysAndNothingDestructiveOrForeign(t *testing.T) {
	wf, raw := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	var cleanupFound bool
	for _, step := range native.Steps {
		if strings.Contains(step.Run, "deploy/compose/baseline/docker-compose.yml down") {
			cleanupFound = true
			if !strings.Contains(step.If, "always()") {
				t.Fatal("cleanup step must run with if: always()")
			}
			if strings.Contains(step.Run, "down -v") || strings.Contains(step.Run, "--volumes") {
				t.Fatal("cleanup must not destroy volumes")
			}
		}
	}
	if !cleanupFound {
		t.Fatal("cleanup step for the job's own compose project is missing")
	}
	for _, forbidden := range []string{
		"down -v",
		"--volumes",
		"deploy/compose/offline",
		"--env-file .env",
		"GATEWAY_REGRESSION_MODE=native scripts/regression.sh",
		"-tags walletcorefallback",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("workflow must not reference %q", forbidden)
		}
	}
}

var composeRequiredVarPattern = regexp.MustCompile(`\$\{([A-Z0-9_]+):\?`)

// composeRequiredVars returns every variable the baseline compose file demands
// with `:?` interpolation, read straight from the file so a newly required
// variable cannot be forgotten by the workflow (this is the A4-R1 defect class).
func composeRequiredVars(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "compose", "baseline", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read baseline compose: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, match := range composeRequiredVarPattern.FindAllStringSubmatch(string(raw), -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			out = append(out, match[1])
		}
	}
	sort.Strings(out)
	return out
}

func TestPrepareDefinesEveryComposeRequiredVariable(t *testing.T) {
	wf, _ := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	prepare := stepsRunning(native.Steps, ">> \"$GITHUB_ENV\"")
	if len(prepare) != 1 {
		t.Fatalf("want exactly one Prepare step writing GITHUB_ENV, got %d", len(prepare))
	}
	required := composeRequiredVars(t)
	if len(required) == 0 {
		t.Fatal("no required compose variables found — interpolation pattern drift")
	}
	runtimeRequired := false
	for _, name := range required {
		if name == "P0_RUNTIME_IMAGE" {
			runtimeRequired = true
		}
		if strings.HasPrefix(name, "POSTGRES_") {
			// Supplied by the generated synthetic env file, read by compose
			// through --env-file interpolation; never echoed by the workflow.
			continue
		}
		if !strings.Contains(prepare[0].Run, name+"=") {
			t.Fatalf("Prepare step does not define compose-required variable %s", name)
		}
	}
	if !runtimeRequired {
		t.Fatal("expected P0_RUNTIME_IMAGE to be a required compose variable")
	}
}

func TestPreflightValidatesInterpolationBeforeBuild(t *testing.T) {
	wf, _ := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	preflightIndex, buildxIndex, buildIndex := -1, -1, -1
	for index, step := range native.Steps {
		if strings.Contains(step.Run, "config --quiet") {
			preflightIndex = index
		}
		if strings.HasPrefix(step.Uses, "docker/setup-buildx-action@") {
			buildxIndex = index
		}
		if strings.HasPrefix(step.Uses, "docker/build-push-action@") {
			buildIndex = index
		}
	}
	if preflightIndex == -1 {
		t.Fatal("compose interpolation preflight step is missing")
	}
	if buildxIndex == -1 {
		t.Fatal("docker/setup-buildx-action step is missing")
	}
	if buildIndex == -1 {
		t.Fatal("native image build step is missing")
	}
	if preflightIndex >= buildxIndex || buildxIndex >= buildIndex {
		t.Fatalf("order must be preflight < buildx setup < image build (got %d, %d, %d)",
			preflightIndex, buildxIndex, buildIndex)
	}
	run := native.Steps[preflightIndex].Run
	for _, want := range []string{
		"deploy/compose/baseline/docker-compose.yml",
		"--env-file \"$P0_ENV_FILE\"",
		"config --quiet",
	} {
		if !strings.Contains(run, want) {
			t.Fatalf("preflight step missing %q", want)
		}
	}
	for _, forbidden := range []string{"config --format", "config$", "config \\"} {
		if strings.Contains(run, forbidden) {
			t.Fatalf("preflight must not print the resolved config (%q)", forbidden)
		}
	}
}

func TestSelfTestsAndEnvHandling(t *testing.T) {
	wf, raw := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	selfTests := stepsRunning(native.Steps, "test_verify_test_json.py")
	if len(selfTests) != 1 || !strings.Contains(selfTests[0].Run, "test_run_tests.sh") {
		t.Fatal("workflow must run the gate unit tests and the runner stub tests")
	}
	prepare := stepsRunning(native.Steps, "generate-offline-env.sh --path")
	if len(prepare) != 1 {
		t.Fatal("workflow must generate the synthetic env with an explicit --path")
	}
	for _, want := range []string{">> \"$GITHUB_ENV\"", "P0_EVIDENCE_DIR=", "P0_DSN_FILE="} {
		if !strings.Contains(prepare[0].Run, want) {
			t.Fatalf("prepare step missing %q", want)
		}
	}
	for _, secret := range []string{"POSTGRES_PASSWORD", "MASTER_KEY", "MNEMONIC_PHRASE", "ADMIN_PASSWORD"} {
		if strings.Contains(raw, "echo \""+secret) || strings.Contains(raw, "${{ secrets."+secret) {
			t.Fatalf("workflow must not echo or inline %q", secret)
		}
	}
}

func TestWorkflowConcurrencySupersedesSameRefOnly(t *testing.T) {
	wf, raw := loadWorkflow(t)
	if err := validateConcurrency(wf.Concurrency); err != nil {
		t.Fatalf("workflow concurrency contract: %v", err)
	}

	// Supersession must replace runs, not skip them: path filters on any
	// trigger are forbidden (the workflow must always evaluate the current
	// checkout).
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil || len(doc.Content) == 0 {
		t.Fatalf("parse workflow document: %v", err)
	}
	root := doc.Content[0]
	triggers, ok := mappingValue(root, "on")
	if !ok || triggers.Kind != yaml.MappingNode {
		t.Fatalf("workflow triggers are missing or not a mapping")
	}
	for index := 0; index+1 < len(triggers.Content); index += 2 {
		name := triggers.Content[index].Value
		filter := triggers.Content[index+1]
		for _, forbidden := range []string{"paths", "paths-ignore"} {
			if _, ok := mappingValue(filter, forbidden); ok {
				t.Fatalf("trigger %q must not use %s (skipping replaces supersession)", name, forbidden)
			}
		}
	}
}

func TestWorkflowConcurrencyContractRejectsMutations(t *testing.T) {
	wf, _ := loadWorkflow(t)
	if err := validateConcurrency(wf.Concurrency); err != nil {
		t.Fatalf("unmutated concurrency must be valid: %v", err)
	}
	cases := map[string]any{
		"missing group":   map[string]any{"cancel-in-progress": true},
		"run id in group": map[string]any{"group": "${{ github.workflow }}-${{ github.run_id }}", "cancel-in-progress": true},
		"sha in group":    map[string]any{"group": "${{ github.workflow }}-${{ github.sha }}", "cancel-in-progress": true},
		"ref missing":     map[string]any{"group": "${{ github.workflow }}", "cancel-in-progress": true},
		"cancel disabled": map[string]any{"group": "${{ github.workflow }}-${{ github.ref }}", "cancel-in-progress": false},
		"string form":     "${{ github.workflow }}-${{ github.ref }}",
		"absent":          nil,
	}
	for name, value := range cases {
		if err := validateConcurrency(value); err == nil {
			t.Fatalf("concurrency mutation %q must be rejected", name)
		}
	}
}

func TestBuildCacheReadsEverywhereWritesOnlyTrustedMain(t *testing.T) {
	wf, _ := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")
	builds := stepsUsing(native.Steps, "docker/build-push-action@")
	if len(builds) != 1 {
		t.Fatalf("want exactly one build action, got %d", len(builds))
	}
	if err := validateCacheInputs(builds[0]); err != nil {
		t.Fatalf("cache contract: %v", err)
	}
}

func TestBuildCacheContractRejectsMutations(t *testing.T) {
	wf, _ := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")
	builds := stepsUsing(native.Steps, "docker/build-push-action@")
	if len(builds) != 1 {
		t.Fatalf("want exactly one build action, got %d", len(builds))
	}
	build := builds[0]
	if err := validateNativeImageStep(build); err != nil {
		t.Fatalf("unmutated build step must be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*workflowStep)
	}{
		{"missing load", func(step *workflowStep) { delete(step.With, "load") }},
		{"load false", func(step *workflowStep) { step.With["load"] = false }},
		{"git context", func(step *workflowStep) {
			step.With["context"] = "https://github.com/xtianxx/PayHarbor.git"
		}},
		{"missing context", func(step *workflowStep) { delete(step.With, "context") }},
		{"push enabled", func(step *workflowStep) { step.With["push"] = true }},
		{"registry tags", func(step *workflowStep) { step.With["tags"] = "ghcr.io/xtianxx/payharbor:test" }},
		{"wrong target", func(step *workflowStep) { step.With["target"] = "runtime" }},
		{"multi platform", func(step *workflowStep) { step.With["platforms"] = "linux/amd64,linux/arm64" }},
		{"missing cache read", func(step *workflowStep) { delete(step.With, "cache-from") }},
		{"foreign cache scope", func(step *workflowStep) { step.With["cache-from"] = "type=gha,scope=other" }},
		{"unconditional cache write", func(step *workflowStep) {
			step.With["cache-to"] = "type=gha,mode=max,scope=" + ghaCacheScope
		}},
		{"branch-only cache write", func(step *workflowStep) {
			step.With["cache-to"] = "${{ github.event_name != 'pull_request' && 'type=gha,mode=max,scope=" + ghaCacheScope + "' || '' }}"
		}},
		{"cacheonly", func(step *workflowStep) { step.With["cache-to"] = "type=gha,mode=cacheonly,scope=" + ghaCacheScope }},
		{"or bypasses main", func(step *workflowStep) {
			// Contains every fragment of the approved expression while letting
			// a non-main ref export the cache.
			step.With["cache-to"] = "${{ github.ref == 'refs/heads/main' || github.event_name != 'pull_request' && 'type=gha,mode=max,scope=" + ghaCacheScope + "' || '' }}"
		}},
		{"else still writes", func(step *workflowStep) {
			// The false branch exports the shared scope instead of an empty
			// string, so untrusted refs still write the cache.
			step.With["cache-to"] = "${{ github.ref == 'refs/heads/main' && github.event_name != 'pull_request' && 'type=gha,mode=max,scope=" + ghaCacheScope + "' || 'type=gha,mode=max,scope=" + ghaCacheScope + "' || '' }}"
		}},
		{"scope suffix spoof", func(step *workflowStep) {
			// The approved scope only appears as a prefix of a different scope.
			step.With["cache-to"] = "${{ github.ref == 'refs/heads/main' && github.event_name != 'pull_request' && 'type=gha,mode=max,scope=" + ghaCacheScope + "-evil' || '' }}"
		}},
		{"foreign Dockerfile", func(step *workflowStep) { step.With["file"] = "../foreign/Dockerfile" }},
		{"no-cache", func(step *workflowStep) { step.With["no-cache"] = true }},
		{"build record upload", func(step *workflowStep) { step.Env["DOCKER_BUILD_RECORD_UPLOAD"] = "true" }},
		{"shell build", func(step *workflowStep) { step.Run = "docker build --target gotest -t test ." }},
		{"fallback tag", func(step *workflowStep) {
			step.With["build-args"] = "BUILD_JOBS=${{ env.BUILD_JOBS }} -tags walletcorefallback"
		}},
	}
	for _, tc := range cases {
		mutated := cloneStep(build)
		tc.mutate(&mutated)
		if err := validateNativeImageStep(mutated); err == nil {
			t.Errorf("build mutation %q must be rejected", tc.name)
		}
	}
}

func TestOnlyExplicitEvidenceArtifactProducer(t *testing.T) {
	wf, raw := loadWorkflow(t)
	native := requireJob(t, wf, "native-regression")

	uploads := stepsUsing(native.Steps, "actions/upload-artifact@")
	if len(uploads) != 1 {
		t.Fatalf("native job must have exactly one artifact upload step, got %d", len(uploads))
	}
	for name, job := range wf.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Uses, "upload-artifact") && name != "native-regression" {
				t.Fatalf("job %q must not upload artifacts", name)
			}
		}
	}
	if strings.Contains(raw, "docker/login-action") {
		t.Fatal("workflow must not log in to a registry")
	}
}
