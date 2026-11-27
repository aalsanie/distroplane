#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import shutil
import tempfile


def invoke(cli, cwd, env, *args, expected=0):
    result = subprocess.run([str(cli), *map(str, args)], cwd=cwd, env=env,
                            capture_output=True, text=True, encoding="utf-8",
                            errors="replace", timeout=45, check=False)
    if result.returncode != expected:
        raise RuntimeError(f"{args[0]}: exit {result.returncode}, expected {expected}: "
                           f"{result.stdout} {result.stderr}")
    return result.stdout


def fresh_install(cli, version, commit=None, exercise_missing=False):
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

        recovery = root / "recovery"
        prepare_recovery(cli, recovery)
        if exercise_missing:
            installed = cli.resolve().parent.parent / "libexec"
            suffix = cli.suffix if os.name == "nt" else ""
            provider = installed / ("distroplane-provider-sdkman" + suffix)
            backup = provider.with_name(provider.name + ".saved")
            provider.rename(backup)
            try:
                config.write_text(json.dumps({
                    "schemaVersion": "1",
                    "release": {"id": "missing-provider", "artifacts": [
                        {"name": "app", "source": str(artifact)}]},
                    "targets": [{"id": "sdkman", "provider": {"name": "sdkman"},
                                 "configuration": configs["sdkman"]}]
                }), encoding="utf-8")
                report = json.loads(invoke(cli, outside, env, "doctor", "--config", config,
                                           "--json", expected=3))
                if not any(c["code"] == "PROVIDER_NOT_FOUND" for c in report["checks"]):
                    raise RuntimeError("missing packaged provider was not detected")
                shutil.copy2(installed / ("distroplane-provider-npm" + suffix), provider)
                report = json.loads(invoke(cli, outside, env, "doctor", "--config", config,
                                           "--json", expected=3))
                if not any(c["code"] == "VERSION_MISMATCH" for c in report["checks"]):
                    raise RuntimeError("wrong packaged provider identity was not detected")
                verify_recovery(cli, recovery)
            finally:
                provider.unlink(missing_ok=True)
                backup.rename(provider)
        else:
            verify_recovery(cli, recovery)
        print(f"Fresh installed workflow passed: {version}")


def recovery_environment(root):
    outside = root / "outside"
    empty_path = root / "empty PATH"
    for directory in (outside, empty_path):
        directory.mkdir(exist_ok=True)
    env = {key: value for key, value in os.environ.items()
           if key.upper() in {"SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR",
                              "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "USERPROFILE",
                              "HOME", "LOCALAPPDATA", "APPDATA"}}
    env["PATH"] = str(empty_path)
    return outside, env


def fingerprint(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def prepare_recovery(cli, root):
    root.mkdir(parents=True, exist_ok=False)
    outside, env = recovery_environment(root)
    artifact = root / "release.zip"
    artifact.write_bytes(b"prebuilt installation fixture")
    config = root / "distroplane.json"
    config.write_text(json.dumps({
        "schemaVersion": "1",
        "release": {"id": "install-recovery", "artifacts": [
            {"name": "app", "source": str(artifact)}]},
        "targets": [{"id": "sdkman", "provider": {"name": "sdkman"},
                     "configuration": {"artifact": "app", "candidate": "example",
                                       "version": "1.2.3",
                                       "url": "https://example.invalid/release.zip",
                                       "authentication": {"mode": "legacy",
                                                          "consumerKeyCredential": "key",
                                                          "consumerTokenCredential": "auth"}}}]
    }), encoding="utf-8")
    pending = json.loads(invoke(cli, outside, env, "publish", "--config", config,
                                "--yes", "--json", expected=4))
    plan = Path(pending["plan"]["path"])
    journal = Path(pending["journalPath"])
    if pending["run"]["targets"][0]["state"] != "BLOCKED":
        raise RuntimeError("missing credential did not block before publication")
    if b"SIDE_EFFECT_DISPATCHED" in journal.read_bytes():
        raise RuntimeError("installation fixture dispatched an external effect")
    files = [plan, journal, *sorted(p for p in (root / ".distroplane/providers").rglob("*")
                                    if p.is_file())]
    if len(files) != 4:
        raise RuntimeError("missing retained provider executable or receipt")
    (root / "recovery-check.json").write_text(json.dumps({
        "plan": str(plan), "journal": str(journal),
        "planId": pending["plan"]["planId"], "runId": pending["run"]["runId"],
        "files": {str(path): fingerprint(path) for path in files}
    }), encoding="utf-8")


def verify_recovery(cli, root):
    outside, env = recovery_environment(root)
    expected = json.loads((root / "recovery-check.json").read_text(encoding="utf-8"))
    for path, digest in expected["files"].items():
        if fingerprint(Path(path)) != digest:
            raise RuntimeError("installation changed recovery bytes: " + path)
    config = root / "distroplane.json"
    report = json.loads(invoke(cli, outside, env, "doctor", "--config", config,
                               "--plan", expected["plan"], "--json", expected=3))
    if not any(c["code"] == "PROVIDER_DIGEST_MATCH" for c in report["checks"]):
        raise RuntimeError("doctor could not verify the retained provider")
    for path, digest in expected["files"].items():
        if fingerprint(Path(path)) != digest:
            raise RuntimeError("read-only doctor changed release history")
    resumed = json.loads(invoke(cli, outside, env, "resume", "--config", config,
                                "--reconcile-only", "--json", expected=4))
    if (resumed["planId"] != expected["planId"] or resumed["runId"] != expected["runId"] or
            resumed["targets"][0]["state"] != "BLOCKED"):
        raise RuntimeError("read-only recovery changed the original run")
    if b"SIDE_EFFECT_DISPATCHED" in Path(expected["journal"]).read_bytes():
        raise RuntimeError("installation fixture dispatched an external effect")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cli", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--prepare-recovery", type=Path)
    mode.add_argument("--verify-recovery", type=Path)
    args = parser.parse_args()
    if args.prepare_recovery:
        prepare_recovery(args.cli.absolute(), args.prepare_recovery.absolute())
    elif args.verify_recovery:
        verify_recovery(args.cli.absolute(), args.verify_recovery.absolute())
    else:
        fresh_install(args.cli, args.version, args.commit)


if __name__ == "__main__":
    main()
