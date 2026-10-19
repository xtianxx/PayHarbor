// Package baseline holds contract tests for the A3/A4 baseline tooling.
//
// This file pins the CI wiring of .github/workflows/regression.yml: the native
// job must build the container image from the current checkout, run the Go
// suite against a dedicated PostgreSQL through scripts/baseline/run-tests.sh,
// and make the fail-closed gate the single required conclusion. The fallback
// job stays supplementary and can never mask the native result.
package baseline

import (
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
	With            map[string]any `yaml:"with"`
}

type workflowJob struct {
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Env            map[string]string `yaml:"env"`
	Steps          []workflowStep    `yaml:"steps"`
}

type workflowFile struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

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

	builds := stepsRunning(native.Steps, "--target gotest")
	if len(builds) != 1 {
		t.Fatalf("want exactly one gotest image build step, got %d", len(builds))
	}
	build := builds[0].Run
	for _, want := range []string{"docker build", "-t \"$P0_TEST_IMAGE\"", "BUILD_JOBS"} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step missing %q", want)
		}
	}
	if strings.Contains(build, "walletcorefallback") {
		t.Fatal("native build must not use the fallback tag")
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

	preflightIndex, buildIndex := -1, -1
	for index, step := range native.Steps {
		if strings.Contains(step.Run, "config --quiet") {
			preflightIndex = index
		}
		if strings.Contains(step.Run, "--target gotest") {
			buildIndex = index
		}
	}
	if preflightIndex == -1 {
		t.Fatal("compose interpolation preflight step is missing")
	}
	if buildIndex == -1 {
		t.Fatal("native image build step is missing")
	}
	if preflightIndex >= buildIndex {
		t.Fatalf("preflight must run before the heavy build (preflight=%d build=%d)", preflightIndex, buildIndex)
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
