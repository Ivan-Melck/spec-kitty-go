#!/usr/bin/env python3
"""Benchmark two installed CLIs on Linux, without touching the working project.

Example:
  python3 scripts/benchmark-rc7.py --go-bin /tmp/spec-kitty \
    --python-bin ~/.cache/spec-kitty-parity/rc7-1b0fe9fc38/oracle-bin/spec-kitty \
    --output /tmp/rc7-results

Build the Go executable first; build/install time is deliberately excluded.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import shutil
import signal
import statistics
import subprocess
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
SLUG = "calc-ops-01M54Y16"


def environment(home, temp):
    home.mkdir(parents=True, exist_ok=True)
    temp.mkdir(parents=True, exist_ok=True)
    return {
        "PATH": os.environ["PATH"], "HOME": str(home),
        "XDG_CONFIG_HOME": str(home / ".config"), "TMPDIR": str(temp),
        "SPEC_KITTY_NO_UPGRADE_CHECK": "1",
        "SPEC_KITTY_GO_PYPI_JSON_URL": "http://127.0.0.1:9/pypi/%s/json",
        "NO_COLOR": "1", "TERM": "dumb", "COLUMNS": "200",
        "LANG": "C.UTF-8", "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_AUTHOR_NAME": "Benchmark", "GIT_AUTHOR_EMAIL": "bench@example.invalid",
        "GIT_COMMITTER_NAME": "Benchmark", "GIT_COMMITTER_EMAIL": "bench@example.invalid",
    }


def checked(args, cwd, env):
    return subprocess.run(args, cwd=cwd, env=env, check=True,
                          capture_output=True, text=True, timeout=120).stdout


def git(cwd, env, *args):
    return checked(["git", *args], cwd, env)


def measure(launcher, binary, args, cwd, env):
    # Files avoid pipe backpressure. The small C launcher measures the child
    # directly, excluding Python spawn overhead and inherited Python RSS.
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        metrics = cwd.parent / "metrics.json"
        proc = subprocess.Popen([str(launcher), str(metrics), str(binary), *args], cwd=cwd, env=env,
                                stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                                start_new_session=True)

        def timeout(signum, frame):
            os.killpg(proc.pid, signal.SIGKILL)
            raise TimeoutError(f"Benchmark exceeded 120 seconds: {args}")

        previous = signal.signal(signal.SIGALRM, timeout)
        signal.alarm(120)
        try:
            _, status = os.waitpid(proc.pid, 0)
            proc.returncode = os.waitstatus_to_exitcode(status)
        finally:
            signal.alarm(0)
            signal.signal(signal.SIGALRM, previous)
            if proc.returncode is None:
                proc.wait()
        out.seek(0)
        err.seek(0)
        stdout, stderr = out.read().decode(errors="replace"), err.read().decode(errors="replace")
        if proc.returncode:
            raise RuntimeError(f"{binary} {args}: exit {proc.returncode}\n{stdout}\n{stderr}")
        return json.loads(metrics.read_text()), stdout, stderr


def tree_bytes(root):
    # Logical bytes, count hardlinked files once, do not follow symlinks.
    seen, total = set(), 0
    for directory, _, files in os.walk(root):
        for name in files:
            path = Path(directory) / name
            stat = path.lstat()
            identity = (stat.st_dev, stat.st_ino)
            if not path.is_symlink() and identity not in seen:
                seen.add(identity)
                total += stat.st_size
    return total


def summary(samples):
    walls = sorted(s["wall_ms"] for s in samples)
    return {
        "n": len(samples), "median_ms": statistics.median(walls),
        "mean_ms": statistics.mean(walls),
        "stdev_ms": statistics.stdev(walls) if len(walls) > 1 else 0,
        "min_ms": min(walls), "max_ms": max(walls),
        "p95_ms": walls[max(0, int(len(walls) * .95 + .999999) - 1)],
        "median_cpu_ms": statistics.median(s["user_ms"] + s["system_ms"] for s in samples),
        "median_max_rss_mib": statistics.median(s["max_rss_kib"] for s in samples) / 1024,
    }


def write_report(results, output):
    """Write a self-contained interactive report alongside the raw samples."""
    page = r'''<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Spec Kitty RC7 performance comparison</title>
<style>
*{box-sizing:border-box}body{margin:0;color:var(--foreground,#20252b);font:14px/1.5 var(--font-sans,system-ui,sans-serif)}
h2{font-size:23px;line-height:1.25;margin:0 0 8px}p{margin:8px 0 14px}.muted,small{color:var(--muted-foreground,#69717b)}
.controls{display:flex;flex-wrap:wrap;gap:6px;margin:18px 0 10px}button{font:inherit;font-size:13px;padding:6px 11px;border:1px solid var(--border,#ccd0d5);border-radius:7px;background:transparent;color:inherit;cursor:pointer}
button[aria-pressed="true"]{background:var(--primary,#27394c);color:var(--primary-foreground,#fff);border-color:transparent}
table{width:100%;border-collapse:collapse;font-variant-numeric:tabular-nums}th{font-size:12px;text-align:right;color:var(--muted-foreground,#69717b);font-weight:500}
td,th{padding:9px 7px;border-bottom:1px solid var(--border,#ddd)}th:first-child,td:first-child{text-align:left;padding-left:0}td{text-align:right}td:last-child,th:last-child{padding-right:0}
.ratio{color:var(--success,#21734b);font-weight:650}.cards{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin:20px 0}
.card{padding:16px;border-radius:var(--radius,9px);background:var(--secondary,#f1f3f5)}.card strong{display:block;font-size:19px;margin:3px 0}.card p{font-size:12px;margin:3px 0}
details{border-top:1px solid var(--border,#ddd);padding-top:12px;margin-top:16px}summary{cursor:pointer}code,pre{font-family:var(--font-mono,monospace);font-size:11px}pre{white-space:pre-wrap;overflow-wrap:anywhere;padding:14px;background:var(--code-background,#f2f3f5);border-radius:8px}
@media(max-width:460px){body{font-size:12px}h2{font-size:20px}td,th{padding:8px 4px}.cards{gap:8px}.card{padding:16px}.card strong{font-size:16px}}
</style>
<h2 id="headline"></h2>
<p class="muted" id="subtitle"></p>
<div class="controls" role="group" aria-label="Measurement">
<button data-metric="median_ms" aria-pressed="true">Elapsed time</button>
<button data-metric="median_cpu_ms" aria-pressed="false">CPU time</button>
<button data-metric="median_max_rss_mib" aria-pressed="false">Peak memory</button>
</div>
<table><thead><tr><th>Operation</th><th id="py-head">Python</th><th id="go-head">Go</th><th id="ratio-head">Faster</th></tr></thead><tbody id="rows"></tbody></table>
<p class="muted" id="measurement-note"></p>
<div class="cards">
<div class="card"><small>Installation footprint</small><strong id="size-main"></strong><p>Go binary / Python virtual environment</p><p id="size-detail"></p></div>
<div class="card"><small>Validation</small><strong id="sample-count"></strong><p>Every timed command exited successfully; structured responses and created artifacts checked.</p></div>
</div>
<details><summary>Method, revisions, and limits</summary><div id="method"></div>
<p>Build and reproduce from the repository root:</p>
<pre>CGO_ENABLED=0 go build -trimpath -o /var/tmp/spec-kitty-benchmark ./cmd/spec-kitty
python3 scripts/benchmark-rc7.py \
  --go-bin /var/tmp/spec-kitty-benchmark \
  --python-bin "$HOME/.cache/spec-kitty-parity/rc7-1b0fe9fc38/oracle-bin/spec-kitty" \
  --output /var/tmp/spec-kitty-benchmark-results</pre>
<p class="muted">Requires Linux, Python 3, a C compiler, Git, and the RC7 reference virtual environment. Raw samples and captured output accompany this report.</p>
</details>
<script>
const data=__BENCHMARK_DATA__;
const names={'version':'Version','help':'Help','config-list':'Agent config list','verify-setup':'Verify setup','charter-status':'Charter status · no charter','tasks-status':'Task status · 2 WPs','next-query-analysis-guard':'Next query · analysis guard','init-warm-home':'Init · existing home','init-fresh-home':'Init · fresh home','mission-create':'Create mission'};
const rows=data.benchmarks, fmt=n=>n.toLocaleString('en',{minimumFractionDigits:1,maximumFractionDigits:1});
const project=rows.filter(r=>!['version','help','next-query-analysis-guard'].includes(r.name));
document.querySelector('#headline').textContent=`Go is ${fmt(Math.min(...project.map(r=>r.speedup)))}–${fmt(Math.max(...project.map(r=>r.speedup)))}× faster on these project operations`;
function show(metric){
 const memory=metric==='median_max_rss_mib',unit=memory?'MiB':'ms';
 document.querySelector('#rows').replaceChildren();
 for(const row of rows){const tr=document.createElement('tr');const values=[names[row.name],fmt(row.summary.python[metric])+' '+unit,fmt(row.summary.go[metric])+' '+unit,fmt(row.summary.python[metric]/row.summary.go[metric])+'×'];
 values.forEach((value,i)=>{const td=document.createElement('td');td.textContent=value;if(i===0)td.title='spec-kitty '+row.args.join(' ');if(i===3)td.className='ratio';tr.append(td)});document.querySelector('#rows').append(tr)}
 document.querySelector('#ratio-head').textContent=memory?'Lower RSS':'Faster';
 document.querySelector('#measurement-note').textContent=memory?'Median per-invocation peak RSS from Linux wait4; includes reaped descendants, not simultaneous process-tree memory.':metric==='median_cpu_ms'?'Median user + system CPU time, including reaped descendants.':`Median wall time. ${data.metadata.read_runs} measured runs per read command; ${data.metadata.write_runs} per init/create command; ${data.metadata.warmups} warmups per executable per operation.`;
 document.querySelectorAll('button[data-metric]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.metric===metric)));
}
document.querySelectorAll('button[data-metric]').forEach(b=>b.addEventListener('click',()=>show(b.dataset.metric)));
const mib=n=>fmt(n/1048576)+' MiB';
document.querySelector('#size-main').textContent=mib(data.sizes.go_binary_bytes)+' / '+mib(data.sizes.python_venv_bytes);
document.querySelector('#size-detail').textContent='Python also uses a '+mib(data.sizes.python_shared_interpreter_bytes)+' shared interpreter. Generated home assets excluded from these sizes.';
document.querySelector('#sample-count').textContent=rows.reduce((n,r)=>n+r.summary.go.n+r.summary.python.n,0)+' measured invocations';
const meta=data.metadata,method=document.querySelector('#method');
document.querySelector('#subtitle').textContent='Upstream Python RC7 vs this Go RC7 checkout · local CLI benchmarks · '+meta.started_at.slice(0,10);
for(const text of [meta.cpu+' · '+meta.platform, 'Go commit '+meta.go_commit+'; upstream Python '+meta.python_reference_revision+'; '+meta.python_runtime+'.',meta.method,meta.limitations,'Version/help and next-query skip much of project startup; their speedups do not predict implementation or full mission throughput.']){const p=document.createElement('p');p.textContent=text;method.append(p)}
show('median_ms');
console.log(JSON.stringify({benchmarkRows:rows.length,measuredInvocations:rows.reduce((n,r)=>n+r.summary.go.n+r.summary.python.n,0),allCommandsPassed:rows.every(r=>r.all_commands_passed)}));
</script></html>'''
    report_data = {**results, "benchmarks": [
        {**{key: value for key, value in row.items() if key != "samples"},
         "all_commands_passed": all(sample["exit_code"] == 0
                                    for samples in row["samples"].values() for sample in samples)}
        for row in results["benchmarks"]]}
    payload = json.dumps(report_data).replace("<", "\\u003c")
    (output / "report.html").write_text(page.replace("__BENCHMARK_DATA__", payload))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-bin", required=True, type=Path)
    parser.add_argument("--python-bin", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--runs", type=int, default=25)
    parser.add_argument("--write-runs", type=int, default=10)
    parser.add_argument("--warmups", type=int, default=3)
    parser.add_argument("--scratch-parent", type=Path, default=Path("/var/tmp"))
    options = parser.parse_args()
    if min(options.runs, options.write_runs) < 1 or options.warmups < 0:
        parser.error("runs must be positive; warmups must be nonnegative")
    options.output.mkdir(parents=True, exist_ok=True)
    binaries = {"go": options.go_bin.resolve(), "python": options.python_bin.resolve()}
    reference = binaries["python"].parent.parent
    rng = random.Random(20261025)
    results = {
        "metadata": {
            "started_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
            "platform": platform.platform(), "cpu_count": os.cpu_count(),
            "load_average_start": list(os.getloadavg()),
            "go_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
            "go_build": subprocess.check_output(["go", "version", "-m", str(binaries["go"])], text=True),
            "go_binary_sha256": hashlib.sha256(binaries["go"].read_bytes()).hexdigest(),
            "python_reference_revision": (reference.parent / "UPSTREAM_REVISION").read_text().strip()
                if (reference.parent / "UPSTREAM_REVISION").exists() else None,
            "python_runtime": subprocess.check_output([str(reference / "bin/python"), "--version"], text=True).strip(),
            "binaries": {k: str(v) for k, v in binaries.items()},
            "read_runs": options.runs, "write_runs": options.write_runs,
            "warmups": options.warmups, "random_seed": 20261025,
            "method": "Sequential randomized pairs; small C launcher measures fork/exec through wait4; warm OS caches; no shell or package launcher; fresh fixtures copied outside timing; all commands must exit zero.",
            "limitations": "Shared desktop host; not CPU pinned; fresh home does not mean dropped OS caches. Peak RSS includes reaped descendants and is not simultaneous process-tree memory. Disk sizes are logical bytes with hardlinks counted once; shared interpreter and generated HOME assets are listed separately. Fresh Codex project without an active charter; mission queries use a two-WP fixture. Next query exercises the missing-analysis guard, not implementation dispatch. No remote/network workflows or build/install timing.",
        }, "sizes": {}, "benchmarks": [],
    }
    cpuinfo = Path("/proc/cpuinfo").read_text()
    results["metadata"]["cpu"] = next(line.split(":", 1)[1].strip() for line in cpuinfo.splitlines() if line.startswith("model name"))
    results["sizes"] = {"go_binary_bytes": binaries["go"].stat().st_size,
                         "python_venv_bytes": tree_bytes(reference)}
    interpreter_root = subprocess.check_output(
        [str(reference / "bin/python"), "-c", "import sys; print(sys.base_prefix)"], text=True).strip()
    results["metadata"]["python_interpreter_root"] = interpreter_root
    results["sizes"]["python_shared_interpreter_bytes"] = tree_bytes(Path(interpreter_root))
    with tempfile.TemporaryDirectory(prefix="sk-rc7-bench-", dir=options.scratch_parent) as scratch:
        scratch = Path(scratch)
        launcher = scratch / "benchmark-exec"
        subprocess.run(["cc", "-O2", "-Wall", "-Wextra", "-o", str(launcher),
                        str(ROOT / "scripts/benchmark-exec.c")], check=True)
        shared_temp = scratch / "tmp"
        empty = scratch / "empty"
        empty.mkdir()
        homes, envs = {}, {}
        for side, binary in binaries.items():
            homes[side] = scratch / f"home-{side}"
            envs[side] = environment(homes[side], shared_temp)
            version = checked([str(binary), "--version"], empty, envs[side]).strip()
            results["metadata"][f"{side}_version"] = version
            if "4.0.0rc7" not in version:
                raise RuntimeError(f"Not RC7: {side}: {version}")
        # One Python-initialized project is the source for both sides, matching
        # the repository's differential tests. The user checkout is never run.
        project = scratch / "project-template"
        project.mkdir()
        git(project, envs["python"], "init", "-q", "-b", "main")
        checked([str(binaries["python"]), "init", ".", "--ai", "codex", "--non-interactive"], project, envs["python"])
        (project / "src").mkdir()
        (project / "src/calc.py").write_text("def add(a, b):\n    return a + b\n")
        git(project, envs["python"], "add", "-A")
        git(project, envs["python"], "-c", "commit.gpgsign=false", "commit", "-qm", "benchmark project")
        git(project, envs["python"], "switch", "-qc", "feat/calc-ops")
        mission_project = scratch / "mission-template"
        shutil.copytree(project, mission_project)
        mission_dir = mission_project / "kitty-specs" / SLUG
        fixture = ROOT / "internal/testkit/missionfx/testdata/calc"
        mission_dir.mkdir(parents=True)
        for filename in ("spec.md", "plan.md", "tasks.md"):
            shutil.copy2(fixture / "authored" / filename, mission_dir / filename)
        for filename in ("lanes.json", "status.events.jsonl", "status.json", "meta.json",
                         "tasks/WP01-subtract-helper.md", "tasks/WP02-multiply.md"):
            target = mission_dir / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(fixture / "finalized" / filename, target)
        meta = json.loads((mission_dir / "meta.json").read_text())
        meta.pop("coordination_branch", None)
        meta["topology"] = "lanes"
        (mission_dir / "meta.json").write_text(json.dumps(meta, indent=2) + "\n")
        git(mission_project, envs["python"], "add", "-A")
        git(mission_project, envs["python"], "-c", "commit.gpgsign=false", "commit", "-qm", "benchmark mission")

        workloads = [
            ("version", ["--version"], "empty", False, False),
            ("help", ["--help"], "empty", False, False),
            ("config-list", ["agent", "config", "list"], "project", False, False),
            ("verify-setup", ["verify-setup"], "project", False, False),
            ("charter-status", ["charter", "status", "--json"], "project", False, False),
            ("tasks-status", ["agent", "tasks", "status", "--mission", SLUG, "--json"], "mission", False, False),
            ("next-query-analysis-guard", ["next", "--mission", SLUG, "--json"], "mission", False, False),
            ("init-warm-home", ["init", ".", "--ai", "codex", "--non-interactive"], "empty", True, False),
            ("init-fresh-home", ["init", ".", "--ai", "codex", "--non-interactive"], "empty", True, True),
            ("mission-create", ["agent", "mission", "create", "benchmark-mission", "--topology", "lanes", "--json"], "project", True, False),
        ]
        for name, args, source, writing, cold in workloads:
            count = options.write_runs if writing else options.runs
            row = {"name": name, "args": args, "fresh_home": cold, "samples": {"go": [], "python": []}}
            samples_dir = options.output / "command-output" / name
            samples_dir.mkdir(parents=True, exist_ok=True)
            print(f"Running {name}: {count} measured pairs + {options.warmups} warmups", flush=True)
            for iteration in range(-options.warmups, count):
                order = list(binaries)
                rng.shuffle(order)
                for side in order:
                    work = scratch / "current-run"
                    work.mkdir()
                    try:
                        cwd = work / "project"
                        if source == "empty":
                            cwd.mkdir()
                        else:
                            shutil.copytree(mission_project if source == "mission" else project, cwd)
                        env = environment(work / "home", shared_temp) if cold else envs[side]
                        sample, stdout, stderr = measure(launcher, binaries[side], args, cwd, env)
                        # Validate meaningful responses, not just exit codes.
                        if name in {"charter-status", "tasks-status", "next-query-analysis-guard", "mission-create"}:
                            json.loads(stdout)
                        if name == "next-query-analysis-guard":
                            response = json.loads(stdout)
                            if response.get("kind") != "query" or response.get("error_code") != "ANALYSIS_REPORT_MISSING":
                                raise RuntimeError("Unexpected next query result")
                        if name == "tasks-status":
                            if json.loads(stdout).get("total_wps") != 2:
                                raise RuntimeError("Expected exactly two work packages")
                            for wp in ("WP01", "WP02"):
                                if wp not in stdout:
                                    raise RuntimeError(f"Missing {wp} in status response")
                        if name.startswith("init-") and not (cwd / ".kittify/config.yaml").is_file():
                            raise RuntimeError("Init did not create project configuration")
                        if name == "mission-create" and not list((cwd / "kitty-specs").glob("*/meta.json")):
                            raise RuntimeError("Mission create did not create mission metadata")
                        if iteration >= 0:
                            sample["iteration"] = iteration
                            row["samples"][side].append(sample)
                        if iteration == 0:
                            (samples_dir / f"{side}.stdout").write_text(stdout)
                            (samples_dir / f"{side}.stderr").write_text(stderr)
                    finally:
                        shutil.rmtree(work)
            row["summary"] = {side: summary(samples) for side, samples in row["samples"].items()}
            row["speedup"] = row["summary"]["python"]["median_ms"] / row["summary"]["go"]["median_ms"]
            results["benchmarks"].append(row)
            (options.output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
            print(f"  Python {row['summary']['python']['median_ms']:.1f} ms; Go {row['summary']['go']['median_ms']:.1f} ms; {row['speedup']:.1f}x", flush=True)
        results["sizes"]["warm_home_bytes"] = {side: tree_bytes(home) for side, home in homes.items()}
    results["metadata"]["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    results["metadata"]["load_average_end"] = list(os.getloadavg())
    (options.output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    write_report(results, options.output)


if __name__ == "__main__":
    main()
