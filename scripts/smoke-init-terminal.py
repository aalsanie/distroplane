#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import tempfile
import time


def terminal(cli, root, answers=None, cancel=False):
    master, slave = pty.openpty()
    try:
        process = subprocess.Popen([str(cli), "init"], cwd=root, stdin=slave,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        os.close(slave)
        slave = None
        try:
            if cancel:
                deadline = time.monotonic() + 10
                seen = bytearray()
                while b"destinations (" not in seen:
                    if time.monotonic() >= deadline:
                        raise RuntimeError("terminal prompt did not appear")
                    ready, _, _ = select.select([process.stderr], [], [], 0.2)
                    if ready:
                        data = os.read(process.stderr.fileno(), 4096)
                        if not data:
                            raise RuntimeError("init exited before prompting")
                        seen.extend(data)
                process.send_signal(signal.SIGINT)
                out, err = process.communicate(timeout=10)
                if process.returncode != 1 or b"cancelled" not in err:
                    raise RuntimeError(f"cancellation failed: {process.returncode} {out!r} {err!r}")
                if (root / "distroplane.json").exists():
                    raise RuntimeError("cancelled terminal wrote configuration")
            else:
                os.write(master, answers.encode("utf-8"))
                out, err = process.communicate(timeout=15)
                if process.returncode != 0 or b"Invalid answer" not in err or b"Credential " not in out:
                    raise RuntimeError(f"guided terminal failed: {process.returncode} {out!r} {err!r}")
                config = json.loads((root / "distroplane.json").read_text(encoding="utf-8"))
                if config["destinations"]["sdkman"]["options"]["candidate"] != "sample":
                    raise RuntimeError("corrected metadata was not saved")
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
    finally:
        os.close(master)
        if slave is not None:
            os.close(slave)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cli", type=Path, required=True)
    args = parser.parse_args()
    cli = args.cli.absolute()
    with tempfile.TemporaryDirectory(prefix="guided é space ") as temp:
        root = Path(temp)
        (root / "dist").mkdir()
        (root / "dist" / "release.zip").write_bytes(b"prebuilt fixture")
        with subprocess.Popen([str(cli), "init", "--json"], cwd=root, stdin=subprocess.PIPE,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE) as process:
            try:
                if process.wait(timeout=10) != 2:
                    raise RuntimeError("JSON init did not fail without input")
                error = json.loads(process.stderr.read())
                if error["error"]["kind"] != "invalid_destination":
                    raise RuntimeError("JSON init omitted missing destination diagnostic")
            finally:
                if process.poll() is None:
                    process.kill()
        terminal(cli, root, cancel=True)
        terminal(cli, root, answers="sdkman\nv1\nbad/name\nsample\n1.2.3\nhttps://example.org/release.zip\n")
        if (root / ".distroplane").exists():
            raise RuntimeError("setup unexpectedly created execution state")
    print("Native guided setup and cancellation passed")


if __name__ == "__main__":
    main()
