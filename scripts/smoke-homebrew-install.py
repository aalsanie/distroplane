#!/usr/bin/env python3
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

from homebrew_formula import generate
from smoke_installed import fresh_install


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, timeout=180, **kwargs)


def staged_formula(directory, version):
    directory = directory.resolve()
    source = generate(directory, version)
    source = source.replace("class Distroplane < Formula\n",
                            f'class Distroplane < Formula\n  version "{version}"\n', 1)
    for platform in ("darwin", "linux"):
        for arch in ("amd64", "arm64"):
            name = f"distroplane_{version}_{platform}_{arch}.tar.gz"
            url = f"https://github.com/aalsanie/distroplane/releases/download/v{version}/{name}"
            if source.count(url) != 1:
                raise RuntimeError("missing or ambiguous formula archive URL: " + name)
            source = source.replace(url, (directory / name).as_uri(), 1)
    return source


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    if os.environ.get("GITHUB_ACTIONS") != "true":
        parser.error("run only on a disposable GitHub-hosted runner")
    directory = args.directory.resolve()
    env = dict(os.environ, HOMEBREW_NO_AUTO_UPDATE="1", HOMEBREW_NO_ANALYTICS="1",
               HOMEBREW_NO_INSTALL_CLEANUP="1")
    tap = "local/distroplane-smoke"
    formula = tap + "/distroplane"
    with tempfile.TemporaryDirectory(prefix="distroplane-tap-") as temp:
        repo = Path(temp)
        source = staged_formula(directory, args.version)
        (repo / "Formula").mkdir()
        (repo / "Formula/distroplane.rb").write_text(source, encoding="utf-8")
        run("git", "init", "-q", str(repo))
        run("git", "-C", str(repo), "add", ".")
        run("git", "-C", str(repo), "-c", "user.name=Fixture",
            "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
        run("brew", "tap", tap, str(repo), env=env)
        try:
            run("brew", "install", "--build-from-source", formula, env=env)
            run("brew", "test", formula, env=env)
            prefix = Path(run("brew", "--prefix", formula, env=env,
                              capture_output=True).stdout.strip())
            fresh_install(prefix / "bin/distroplane", args.version, args.commit)
        finally:
            run("brew", "uninstall", "--force", formula, env=env)
            run("brew", "untap", tap, env=env)


if __name__ == "__main__":
    main()
