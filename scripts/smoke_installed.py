#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile


def invoke(cli, cwd, env, *args, expected=0):
    result = subprocess.run([str(cli), *map(str, args)], cwd=cwd, env=env,
                            capture_output=True, text=True, encoding="utf-8",
                            errors="replace", timeout=45, check=False)
    if result.returncode != expected:
        raise RuntimeError(f"{args[0]}: exit {result.returncode}, expected {expected}: "
                           f"{result.stdout} {result.stderr}")
    return result.stdout


def fresh_install(cli, version, commit=None):
    cli = cli.absolute()
    with tempfile.TemporaryDirectory(prefix="distroplane user é space ") as temp:
        root = Path(temp)
        project = root / "project"
        outside = root / "other working directory"
        empty_path = root / "empty PATH"
        for directory in (project, outside, empty_path):
            directory.mkdir()
        env = {key: value for key, value in os.environ.items()
               if key.upper() in {"SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR",
                                  "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "USERPROFILE",
                                  "HOME", "LOCALAPPDATA", "APPDATA"}}
        env["PATH"] = str(empty_path)
        identity = json.loads(invoke(cli, outside, env, "version", "--json"))
        if identity["version"] != version or (commit and identity["commit"] != commit):
            raise RuntimeError("selected CLI identity mismatch")
        help_text = invoke(cli, outside, env, "help")
        for command in ("init", "doctor", "publish", "resume"):
            if command not in help_text:
                raise RuntimeError("missing command in installed CLI: " + command)
        artifact = project / "release.tgz"
        artifact.write_bytes(b"prebuilt installation fixture")
        config = project / "distroplane.json"
        initialized = json.loads(invoke(cli, outside, env, "init", "--release",
                                        "install-check", "--destination", "npm",
                                        "--artifact", artifact, "--output", config,
                                        "--non-interactive", "--json"))
        if Path(initialized["configPath"]) != config:
            raise RuntimeError("init did not write the requested configuration")
        source = json.loads(config.read_text(encoding="utf-8"))
        npm_options = source["destinations"]["npm"]["options"]
        configs = {
            "npm": dict(npm_options, artifact="app"),
            "sdkman": {"artifact": "app", "candidate": "example", "version": "1.2.3",
                       "url": "https://example.invalid/release.tgz",
                       "authentication": {"mode": "legacy", "consumerKeyCredential": "key",
                                          "consumerTokenCredential": "auth"}},
            "homebrew": {"artifact": "app", "authentication": {"credential": "brew"}},
            "winget": {"artifact": "app", "authentication": {"credential": "winget"}},
        }
        for provider, options in configs.items():
            config.write_text(json.dumps({
                "schemaVersion": "1",
                "release": {"id": "install-check-" + provider, "artifacts": [
                    {"name": "app", "source": str(artifact)}]},
                "targets": [{"id": provider, "provider": {"name": provider},
                             "configuration": options}]
            }), encoding="utf-8")
            report = json.loads(invoke(cli, outside, env, "doctor", "--config", config,
                                       "--json", expected=3))
            codes = {check["code"]: check for check in report["checks"]}
            for code in ("ARTIFACT_READABLE", "PROVIDER_FOUND", "PROVIDER_HASHED",
                         "ARCH_VERIFIED", "PROVIDER_VERSION", "REMOTE_SKIPPED"):
                if code not in codes:
                    raise RuntimeError(f"{provider}: missing doctor check {code}: {report}")
            if not codes["PROVIDER_VERSION"]["message"].endswith("version " + version):
                raise RuntimeError(f"{provider}: wrong installed provider identity")
            if "MISSING_CREDENTIAL" not in codes or report["healthy"]:
                raise RuntimeError(f"{provider}: absent credentials reported ready")
            if ("GIT_MISSING" in codes) != (provider in ("homebrew", "winget")):
                raise RuntimeError(f"{provider}: incorrect Git prerequisite")
            if "GIT_AVAILABLE" in codes or "VERSION_MISMATCH" in codes:
                raise RuntimeError(f"{provider}: unexpected tool or provider identity")

        # Use the SDKMAN provider's local planning path and fail before dispatch
        # because the controlled environment contains no credential sources.
        config.write_text(json.dumps({
            "schemaVersion": "1",
            "release": {"id": "install-recovery", "artifacts": [
                {"name": "app", "source": str(artifact)}]},
            "targets": [{"id": "sdkman", "provider": {"name": "sdkman"},
                         "configuration": configs["sdkman"]}]
        }), encoding="utf-8")
        pending = json.loads(invoke(cli, outside, env, "publish", "--config", config,
                                    "--yes", "--json", expected=4))
        plan = Path(pending["plan"]["path"])
        journal = Path(pending["journalPath"])
        if pending["run"]["targets"][0]["state"] != "BLOCKED":
            raise RuntimeError("missing credential did not block before publication")
        before = (plan.read_bytes(), journal.read_bytes())
        report = json.loads(invoke(cli, outside, env, "doctor", "--config", config,
                                   "--plan", plan, "--json", expected=3))
        if not any(c["code"] == "PROVIDER_DIGEST_MATCH" for c in report["checks"]):
            raise RuntimeError("doctor could not verify the retained provider")
        if (plan.read_bytes(), journal.read_bytes()) != before:
            raise RuntimeError("read-only doctor changed release history")
        resumed = json.loads(invoke(cli, outside, env, "resume", "--config", config,
                                    "--reconcile-only", "--json", expected=4))
        if (resumed["planId"] != pending["plan"]["planId"] or
                resumed["runId"] != pending["run"]["runId"] or
                resumed["targets"][0]["state"] != "BLOCKED"):
            raise RuntimeError("read-only recovery changed the original run")
        if b"SIDE_EFFECT_DISPATCHED" in journal.read_bytes():
            raise RuntimeError("installation fixture dispatched an external effect")
        print(f"Fresh installed workflow passed: {version}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cli", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit")
    args = parser.parse_args()
    fresh_install(args.cli, args.version, args.commit)


if __name__ == "__main__":
    main()
