#!/usr/bin/env python3
"""Package SemVer and legacy CalVer releases in an isolated clone, without publishing anything."""
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile


def run(*args, cwd, env=None):
    result = subprocess.run(args, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    if result.returncode:
        raise RuntimeError(result.stdout)
    return result.stdout.strip()


def main():
    root = Path(__file__).resolve().parent.parent
    executable = shutil.which(sys.argv[1] if len(sys.argv) > 1 else "goreleaser")
    if not executable:
        raise RuntimeError("GoReleaser 2.18.3 is required")
    with tempfile.TemporaryDirectory(prefix="memogram-release-") as temp:
        repo = Path(temp) / "repo"
        run("git", "clone", "--quiet", "--no-hardlinks", str(root), str(repo), cwd=root)
        revision = run("git", "rev-parse", "HEAD", cwd=root)
        run("git", "checkout", "--quiet", "--detach", revision, cwd=repo)
        # Include local maintenance edits, while respecting .gitignore so .env
        # and token files never enter the fixture or release archives.
        files = run("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", cwd=root)
        for name in set(files.split("\0")):
            source = root / name
            if not name or not source.is_file():
                continue
            target = repo / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
        for key, value in [("user.name", "Memogram Release Test"), ("user.email", "memogram-test@example.com"), ("commit.gpgsign", "false"), ("tag.gpgsign", "false")]:
            run("git", "config", key, value, cwd=repo)
        run("git", "remote", "set-url", "origin", run("git", "remote", "get-url", "origin", cwd=root), cwd=repo)
        run("git", "add", "--all", cwd=repo)
        run("git", "commit", "--quiet", "--allow-empty", "-m", "test: verify release packaging", cwd=repo)
        commit = run("git", "rev-parse", "HEAD", cwd=repo)
        previous = run("bash", "scripts/release_version.sh", "previous-tag", ".", cwd=repo)
        goos = platform.system().lower()
        goarch = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}[platform.machine().lower()]
        for tag in ["v0.2.0", "v0.2.0-rc.1", "26.01.0"]:
            run("git", "tag", "--force", tag, cwd=repo)
            run("bash", "scripts/release_version.sh", "version", tag, cwd=repo)
            env = os.environ.copy()
            for key in ["GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITEA_TOKEN"]:
                env.pop(key, None)
            # Cross-compilers for several operating systems can exhaust WSL
            # memory and cause swapping. Keep local dry runs sequential.
            env.setdefault("GOMAXPROCS", "2")
            env.setdefault("GOFLAGS", "-p=2")
            env.update(GORELEASER_CURRENT_TAG=tag, GORELEASER_PREVIOUS_TAG=previous)
            run(executable, "release", "--clean", "--parallelism=1", "--skip=publish,announce", cwd=repo, env=env)
            archives = list((repo / "dist").glob("memogram_" + tag + "_*.tar.gz"))
            if not archives:
                raise RuntimeError("missing archives for " + tag)
            native = repo / "dist" / ("memogram_" + tag + "_" + goos + "_" + goarch + ".tar.gz")
            binary = Path(temp) / "memogram"
            with tarfile.open(native) as archive:
                names = {item.name for item in archive.getmembers()}
                expected = {"README.md", "README_NOTEBOOK.md", "CHANGELOG.md", "notebook.example.yaml", ".env.example", ".env.notebook.example"}
                if not expected <= names:
                    raise RuntimeError("missing release setup files: " + str(expected - names))
                if any(Path(name).name in {".env", "notebook.yaml", "data.txt"} for name in names):
                    raise RuntimeError("private configuration or bindings included in release")
                member = next(item for item in archive.getmembers() if Path(item.name).name == "memogram")
                binary.write_bytes(archive.extractfile(member).read())
            binary.chmod(0o700)
            actual = run(str(binary), "--version", cwd=repo)
            if actual != "memogram " + tag.removeprefix("v") + " (commit " + commit + ")":
                raise RuntimeError("archive version mismatch: " + actual)
            print("Release dry run passed: " + tag + ", " + str(len(archives)) + " archives; previous stable " + previous, flush=True)


if __name__ == "__main__":
    main()
