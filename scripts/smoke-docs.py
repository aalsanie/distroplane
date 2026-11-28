#!/usr/bin/env python3
"""Execute documented CLI flows against prebuilt, nonpublishing fixtures."""
import argparse
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def blocks(name, language):
    return re.findall(r"(?ms)^" + chr(96) * 3 + language + r"\n(.*?)^" + chr(96) * 3 + r"$",
                      (ROOT / name).read_text(encoding="utf-8"))


def commands(name):
    return [line for block in blocks(name, "sh") for line in block.splitlines()
            if line.startswith("distroplane ")]


def package(path, version):
    path.parent.mkdir(parents=True, exist_ok=True)
    raw = json.dumps({"name": "example-docs-tool", "version": version}).encode()
    with tarfile.open(path, "w:gz") as archive:
        member = tarfile.TarInfo("package/package.json")
        member.size = len(raw)
        archive.addfile(member, io.BytesIO(raw))


def invoke(argv, project, env, expected=0):
    result = subprocess.run(list(map(str, argv)), cwd=project, env=env,
                            stdin=subprocess.DEVNULL, capture_output=True, text=True,
                            encoding="utf-8", errors="replace", timeout=60)
    if result.returncode != expected:
        raise AssertionError(f"{argv}: expected {expected}, got {result.returncode}\n"
                             f"{result.stdout}\n{result.stderr}")
    return result.stdout


def shell(command, project, env, expected=0):
    if os.name == "nt":
        argv = ["pwsh", "-NoProfile", "-NonInteractive", "-Command",
                command + "; exit $LASTEXITCODE"]
    else:
        argv = ["/bin/sh", "-c", command]
    return invoke(argv, project, env, expected)


def terminal(command, project, env, answers, expected=0):
    import pty
    master, slave = pty.openpty()
    try:
        process = subprocess.Popen(["/bin/sh", "-c", "exec " + command],
                                   cwd=project, env=env, stdin=slave,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        os.close(slave)
        slave = None
        try:
            os.write(master, answers.encode("utf-8"))
            out, err = process.communicate(timeout=60)
            if process.returncode != expected:
                raise AssertionError(f"{command}: expected {expected}, got {process.returncode}\n{out!r}\n{err!r}")
            return out.decode("utf-8", errors="replace")
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
    finally:
        os.close(master)
        if slave is not None:
            os.close(slave)


def state(cli, project, env, release, expected=0):
    return json.loads(invoke([cli, "status", "--release", release, "--json"],
                             project, env, expected))


def approve(cli, project, env, command, interactive, expected):
    preview = json.loads(invoke([cli, "publish", "--dry-run", "--json"], project, env))
    assert not preview["approved"] and "run" not in preview
    plan = preview["plan"]["planId"]
    if interactive:
        terminal(command, project, env, "publish " + plan + "\n", expected)
    else:
        shell(command + " --confirm-plan " + plan, project, env, expected)
    return plan


def quickstart(cli, fake, root):
    install = root / "isolated fixture bin"
    install.mkdir()
    suffix = cli.suffix if os.name == "nt" else ""
    # Only this isolated CLI can resolve the fake npm provider.
    local = install / ("distroplane" + suffix)
    shutil.copy2(cli, local)
    shutil.copy2(fake, install / ("distroplane-provider-npm" + suffix))
    env = dict(os.environ)
    env["PATH"] = str(install) + os.pathsep + env.get("PATH", "")
    env["NPM_TOKEN"] = "documentation-fixture-only"
    readme = commands("README.md")
    assert readme == ["distroplane init", "distroplane doctor", "distroplane publish",
                      "distroplane status", "distroplane resume", "distroplane init --update",
                      "distroplane doctor", "distroplane publish"], readme
    guide = commands("docs/cli.md")
    scripted = [line for line in guide if line.startswith("distroplane init --destination npm ")]
    updates = [line for line in guide if line.startswith("distroplane init --update --release ")]
    assert len(scripted) == len(updates) == 1
    # Native terminal on Unix; the exact noninteractive guide example runs on all hosts.
    for interactive in ([False] if os.name == "nt" else [False, True]):
        project = root / ("terminal project" if interactive else "scripted project")
        project.mkdir()
        first = project / "dist/tool 1.2.3.tgz"
        second = project / "dist/tool-1.2.4.tgz"
        package(first, "1.2.3")
        if interactive:
            terminal(readme[0], project, env, "npm\nv1.2.3\n")
        else:
            shell(scripted[0], project, env)
        config = project / "distroplane.json"
        original = config.read_bytes()
        original_artifact = first.read_bytes()
        assert json.loads(original)["release"]["id"] == "v1.2.3"
        assert not (project / ".distroplane").exists()
        shell(readme[1], project, env)
        if not interactive:
            shell(next(line for line in guide if line == "distroplane publish --dry-run"), project, env)
        missing = dict(env)
        missing.pop("NPM_TOKEN", None)
        # Block before dispatch, then restore the original run with a fresh source.
        plan = approve(local, project, missing, readme[2], interactive, 4)
        shell(readme[3], project, missing, 4)
        pending = state(local, project, missing, "v1.2.3", 4)
        assert pending["planId"] == plan and pending["targets"][0]["state"] == "BLOCKED"
        shell(readme[4], project, env)
        first_state = state(local, project, env, "v1.2.3")
        assert first_state["runId"] == pending["runId"] and first_state["completed"]
        assert first_state["targets"][0]["state"] == "PUBLISHED"
        package(second, "1.2.4")
        if interactive:
            terminal(readme[5], project, env, "v1.2.4\ndist/tool-1.2.4.tgz\nupdate v1.2.4\n")
        else:
            shell(updates[0], project, env)
        assert first.read_bytes() == original_artifact
        assert any(path.read_bytes() == original for path in project.glob("distroplane.json.bak.*"))
        assert json.loads(config.read_bytes())["release"]["id"] == "v1.2.4"
        shell(readme[6], project, env)
        second_plan = approve(local, project, env, readme[7], interactive, 0)
        final = state(local, project, env, "v1.2.4")
        assert final["planId"] == second_plan != plan and final["runId"] != pending["runId"]
        assert final["completed"] and final["targets"][0]["state"] == "PUBLISHED"
        assert state(local, project, env, "v1.2.3") == first_state
        records = json.loads(invoke([local, "runs", "--json"], project, env))["runs"]
        assert len(records) == 2


def guided_example(cli, root, env, variant, artifact, options):
    project = root / ("guided-" + variant.replace(":", "-"))
    project.mkdir()
    dist = project / "dist"
    dist.mkdir()
    target = dist / artifact.name
    shutil.copy2(artifact, target)
    fields = {}
    if variant == "sdkman":
        fields = {f"sdkman.{key}": options[key] for key in ("candidate", "version", "url")}
    elif variant.startswith("homebrew:"):
        manifest = options["manifest"]
        fields = {"homebrew.repository": options["repository"], "homebrew.path": options["path"],
                  "homebrew.name": manifest["name"], "homebrew.description": manifest["description"],
                  "homebrew.homepage": manifest["homepage"], "homebrew.url": manifest["url"],
                  "homebrew.version": manifest["version"]}
        if variant == "homebrew:cask":
            fields.update({"homebrew.install-kind": manifest["installKind"],
                           "homebrew.install-source": manifest["installSource"]})
        else:
            fields["homebrew.binary"] = manifest["binary"]
            if variant == "homebrew:jvm":
                fields["homebrew.java-version"] = manifest["javaVersion"]
    elif variant.startswith("winget:"):
        data = options["package"]
        installer = options["installer"]
        fields = {"winget.repository": options["repository"], "winget.identifier": data["identifier"],
                  "winget.version": data["version"], "winget.publisher": data["publisher"],
                  "winget.name": data["name"], "winget.license": data["license"],
                  "winget.description": data["shortDescription"], "winget.url": installer["url"],
                  "winget.architecture": installer["architecture"],
                  "winget.pr-repository": options["pullRequest"]["repository"]}
        if variant == "winget:zip":
            fields["winget.nested-type"] = installer["nestedInstallerType"]
            fields["winget.nested-files"] = json.dumps(installer["nestedInstallerFiles"], separators=(",", ":"))
    local_env = dict(env)
    local_env["PATH"] = str(cli.parent) + os.pathsep + local_env.get("PATH", "")
    if os.name == "nt":
        args = [cli, "init", "--destination", variant, "--release", "docs-example",
                "--artifact", target, "--non-interactive"]
        for name, value in fields.items():
            args.extend(("--set", name + "=" + value))
        invoke(args, project, local_env)
    else:
        answers = "\n".join([variant, "docs-example", *fields.values()]) + "\n"
        terminal("distroplane init", project, local_env, answers)
    config = json.loads((project / "distroplane.json").read_bytes())
    assert config["schemaVersion"] == "2"
    assert not (project / ".distroplane").exists()
    invoke([cli, "validate"], project, local_env)
    plan = json.loads(invoke([cli, "publish", "--dry-run", "--json"], project, local_env))
    assert plan["plan"]["operations"] == 1 and not plan["approved"]
    assert plan["review"][0]["reviewFields"] and not plan["review"][0]["limited"]
    assert not list((project / ".distroplane").rglob("*.journal"))


def provider_examples(cli, root):
    # Plan only, with actual providers. No example has permission to publish.
    env = dict(os.environ)
    for provider, index, suffix in (("npm", 0, ".tgz"), ("sdkman", 0, ".zip"),
                                   ("homebrew", 0, ".tar.gz"), ("homebrew", 1, ".zip"),
                                   ("winget", 0, ".msi"), ("winget", 1, ".zip")):
        project = root / f"{provider}-{index}"
        project.mkdir()
        artifact = project / ("release" + suffix)
        if provider == "npm":
            package(artifact, "1.2.3")
        elif suffix == ".zip":
            with zipfile.ZipFile(artifact, "w") as archive:
                archive.writestr("bin/acme.exe", b"prebuilt fixture")
                archive.writestr("bin/demo", b"launcher")
                archive.writestr("lib/demo.jar", b"runtime")
        else:
            artifact.write_bytes(b"prebuilt fixture")
        examples = [json.loads(block) for block in blocks(f"providers/{provider}/README.md", "json")]
        options = examples[0]
        if index == 1:
            if provider == "homebrew":
                options["manifest"] = examples[index]
            else:
                options.update(examples[index])
        if provider == "npm":
            options["packagePath"] = str(artifact)
        if provider == "winget" and index == 1:
            options["installer"]["archivePath"] = str(artifact)
        config = {"schemaVersion": "1", "release": {"id": "docs-example",
                  "artifacts": [{"name": options["artifact"], "source": str(artifact)}]},
                  "targets": [{"id": provider, "provider": {"name": provider}, "configuration": options}]}
        (project / "distroplane.json").write_text(json.dumps(config), encoding="utf-8")
        invoke([cli, "validate"], project, env)
        plan = json.loads(invoke([cli, "publish", "--dry-run", "--json"], project, env))
        assert not plan["approved"] and "run" not in plan and plan["plan"]["operations"] == 1
        assert plan["review"][0]["reviewFields"] and not plan["review"][0]["limited"]
        assert not list((project / ".distroplane").rglob("*.journal"))
        variant = provider
        if provider == "homebrew":
            variant += ":jvm" if index else ":formula"
        elif provider == "winget":
            variant += ":zip" if index else ":msi"
        guided_example(cli, root, env, variant, artifact, options)
        if provider == "homebrew" and index == 1:
            cask = dict(options, path="Casks/acme.rb", manifest={
                "name": "acme", "description": "Acme app", "homepage": "https://example.com/acme",
                "url": "https://example.com/acme.zip", "version": "1.2.3",
                "installKind": "app", "installSource": "Acme.app"})
            guided_example(cli, root, env, "homebrew:cask", artifact, cask)

    project = root / "configuration-example"
    project.mkdir()
    config = json.loads(blocks("docs/configuration.md", "json")[0])
    package(project / config["release"]["artifacts"]["package"]["source"], "1.2.3")
    (project / "distroplane.json").write_text(json.dumps(config), encoding="utf-8")
    invoke([cli, "validate"], project, env)
    result = json.loads(invoke([cli, "publish", "--dry-run", "--json"], project, env))
    assert result["plan"]["operations"] == 1 and not result["approved"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cli", type=Path, required=True)
    parser.add_argument("--fake", type=Path, required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="distroplane documented examples ") as tmp:
        root = Path(tmp).resolve()
        quickstart(args.cli.resolve(strict=True), args.fake.resolve(strict=True), root)
        provider_examples(args.cli.resolve(strict=True), root)
    print("Native documentation examples passed")


if __name__ == "__main__":
    main()
