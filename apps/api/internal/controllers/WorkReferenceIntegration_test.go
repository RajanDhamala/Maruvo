package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rajandhamala/Maruvo/internal/work"
)

func TestWorkCLIReferenceWorkflow(t *testing.T) {
	binary := os.Getenv("WORK_TEST_CLI")
	if binary == "" {
		t.Skip("set WORK_TEST_CLI to a matching built CLI for the deterministic two-harness rehearsal")
	}
	f := workFixture(t)
	directory := t.TempDir()
	configuration := filepath.Join(directory, "config")
	for i, name := range []string{"requester", "lead", "verifier"} {
		profile := filepath.Join(configuration, "maruvo", "profiles", name)
		if err := os.MkdirAll(profile, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]string{"api_url": f.server.URL, "token": f.tokens[i]})
		if err := os.WriteFile(filepath.Join(profile, "session.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Keep this fixture separate from real provider profiles, wallets, and signed payments.
	workflowStart := time.Now()
	p := f.workOp(t, 1, "", work.Request{Name: "CLI reference bug fix", PostID: f.postID}, 200).ProjectID
	f.workTeam(t, 1, p, 2)
	_, err := f.pool.Exec(f.ctx, `INSERT INTO post_escrows(post_id,address,program_id,reviewer,network,transaction,last_valid_block_height,fee_lamports,storage_lamports,state) VALUES($1,$2,'fixture','fixture','localnet','fixture',1,0,0,'confirmed')`, f.postID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(f.ctx, "UPDATE posts SET status='in_progress',expected_outputs=ARRAY['patch.diff','tests.log'] WHERE id=$1", f.postID)
	if err != nil {
		t.Fatal(err)
	}
	input := f.workUpload(t, 1, p, "", "", "source.py", "def inc(x):\n    return x\n")
	f.workOp(t, 1, p, work.Request{Action: "plan", Assignments: []work.AssignmentSpec{{ID: "patch", Title: "Fix inc", Instructions: "inc(x) must return x+1. Deliver patch.diff.", Assignee: f.users[1], Inputs: []string{input.ID}, ExpectedOutputs: []string{"patch.diff"}}, {ID: "verify", Title: "Verify exact patch", Instructions: "Apply the accepted patch and verify inc on negative, zero and positive inputs.", Kind: "verification", Assignee: f.users[2], Inputs: []string{input.ID}, Dependencies: []string{"patch"}, ExpectedOutputs: []string{"tests.log"}}}}, 200)
	patchScript := filepath.Join(directory, "patch.py")
	patchCode := `import difflib,json,pathlib,platform,sys
c=json.load(sys.stdin)
s=pathlib.Path(c['inputs'][0]['path']).read_text()
fixed=s.replace('return x','return x + 1')
p=''.join(difflib.unified_diff(s.splitlines(True),fixed.splitlines(True),fromfile='source.py',tofile='source.py'))
pathlib.Path(c['output_directory'],'patch.diff').write_text(p)
pathlib.Path(c['evidence_file']).write_text(json.dumps({'note':'Fixed inc with an exact patch','evidence':[{'command':'diff source.py fixed.py','exit_code':0,'result':'one-line patch generated','environment':platform.platform()}],'usage':{'input_tokens':0,'output_tokens':0}}))
`
	if err = os.WriteFile(patchScript, []byte(patchCode), 0600); err != nil {
		t.Fatal(err)
	}
	verifierScript := filepath.Join(directory, "verify.sh")
	verifierCode := `#!/bin/sh
set -eu
python3 - <<'PY'
import json,os,pathlib,platform,subprocess
c=json.loads(pathlib.Path(os.environ['MARUVO_TASK_FILE']).read_text())
files={i['artifact']['name']:pathlib.Path(i['path']) for i in c['inputs']}
source=pathlib.Path('source.py');source.write_bytes(files['source.py'].read_bytes())
subprocess.run(['patch','--quiet',str(source),str(files['patch.diff'])],check=True)
ns={};exec(source.read_text(),ns)
for n in (-1,0,1):assert ns['inc'](n)==n+1
pathlib.Path(c['output_directory'],'tests.log').write_text('PASS: inc(-1), inc(0), inc(1) after applying the exact accepted patch\n')
pathlib.Path(c['evidence_file']).write_text(json.dumps({'note':'Independent shell harness verified the exact patch','evidence':[{'command':'patch source.py patch.diff; python3 inc assertions','exit_code':0,'result':'3 checks passed','environment':'shell adapter + '+platform.platform()}],'usage':{'input_tokens':0,'output_tokens':0}}))
PY
`
	if err = os.WriteFile(verifierScript, []byte(verifierCode), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(profile, assignment, executable, argument string) time.Duration {
		t.Helper()
		start := time.Now()
		cmd := exec.CommandContext(f.ctx, binary, "-api", f.server.URL, "-profile", profile, "work", "run", "--project", p, "--assignment", assignment, "--operation-id", uuid.NewString(), "--exec", executable, "--arg", argument, "--dir", filepath.Join(directory, profile), "--timeout", "30s")
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configuration)
		// An inherited task token must never replace this fixture's distinct operator identities.
		env := []string{}
		for _, entry := range cmd.Env {
			if len(entry) < len("MARUVO_AGENT_TOKEN_FILE=") || entry[:len("MARUVO_AGENT_TOKEN_FILE=")] != "MARUVO_AGENT_TOKEN_FILE=" {
				env = append(env, entry)
			}
		}
		cmd.Env = env
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s harness: %v\n%s", profile, err, data)
		}
		return time.Since(start)
	}
	patchElapsed := run("lead", "patch", "/usr/bin/python3", patchScript)
	snapshot := f.workView(t, 1, p)
	patch := snapshot.Assignment("patch").Contributions[0]
	f.workAccept(t, 1, p, "patch", patch.ID)
	verificationElapsed := run("verifier", "verify", "/bin/sh", verifierScript)
	snapshot = f.workView(t, 1, p)
	verification := snapshot.Assignment("verify").Contributions[0]
	if verification.Dependencies["patch"] != patch.ID {
		t.Fatal("independent harness verified another patch")
	}
	f.workAccept(t, 1, p, "verify", verification.ID)
	integration := workDecode[work.Integration](t, f.workOp(t, 1, p, work.Request{Action: "integrate", Contributions: []string{patch.ID, verification.ID}, Outputs: append(patch.Outputs, verification.Outputs...), Note: "exact patch and independent test evidence"}, 200).Result)
	f.workOp(t, 1, p, work.Request{Action: "deliver", IntegrationID: integration.ID, SubmissionVersion: 0, Inputs: []string{}, Note: "ready for requester review"}, 200)
	workflowElapsed := time.Since(workflowStart)
	baselineStart := time.Now()
	baseline := filepath.Join(directory, "baseline")
	if err = os.Mkdir(baseline, 0700); err != nil {
		t.Fatal(err)
	}
	baselineCode := `import difflib,pathlib
original="def inc(x):\n    return x\n"
fixed=original.replace('return x','return x + 1')
patch=''.join(difflib.unified_diff(original.splitlines(True),fixed.splitlines(True),fromfile='source.py',tofile='source.py'))
ns={};exec(fixed,ns)
for n in (-1,0,1): assert ns['inc'](n)==n+1
pathlib.Path('patch.diff').write_text(patch)
pathlib.Path('tests.log').write_text('PASS: 3 checks')
`
	cmd := exec.CommandContext(f.ctx, "/usr/bin/python3", "-c", baselineCode)
	cmd.Dir = baseline
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("single-process baseline: %v %s", err, data)
	}
	metrics := map[string]any{"evidence_kind": "deterministic same-host fixture; no live model or payment", "two_harnesses": []string{"Python patch adapter", "shell verification adapter"}, "two_operator_checks_passed": 3, "single_process_baseline_checks_passed": 3, "two_operator_total_elapsed_ms": workflowElapsed.Milliseconds(), "patch_elapsed_ms": patchElapsed.Milliseconds(), "verification_elapsed_ms": verificationElapsed.Milliseconds(), "baseline_elapsed_ms": time.Since(baselineStart).Milliseconds(), "reported_model_input_tokens": 0, "reported_model_output_tokens": 0, "scripted_contribution_review_actions": 2, "human_review_effort": "not measured", "recovery": "separate PostgreSQL restart/expired-attempt tests", "conclusion": "This fixture demonstrates handoffs, not multi-agent superiority."}
	data, _ := json.MarshalIndent(metrics, "", "  ")
	t.Log(string(data))
	if path := os.Getenv("WORK_TEST_REPORT"); path != "" {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var version int64
	if err = f.pool.QueryRow(f.ctx, "SELECT submission_version FROM post_workspaces WHERE post_id=$1", f.postID).Scan(&version); err != nil || version != 1 {
		t.Fatal(fmt.Sprintf("final version %d: %v", version, err))
	}
}
