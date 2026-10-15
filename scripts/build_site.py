#!/usr/bin/env python3
"""Builds the sources of the documentation site (GitHub Pages) into site-src/.

The Markdown in README.md and docs/ is the single source; it is written to
read well on GitHub. For the site this script:
  - makes README.md the home page (index.md),
  - turns links to files outside docs/ (examples/, LICENSE, ...) into links
    to those files on GitHub,
  - drops the "← Documentation · Previous · Next" line at the top of each
    page (the site has its own navigation),
  - publishes the release notes of versions that have a git tag.

    python scripts/build_site.py && mkdocs build --strict
"""
import os
import re
import shutil
import subprocess

REPO = "https://github.com/FadhlanHawali/grepalert"
ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
OUT = os.path.join(ROOT, "site-src")
LINK = re.compile(r"\]\(([^)\s]+)\)")


def rewrite(text, src_dir, out_dir):
    """Rewrites the relative links of a page that lives in src_dir (relative
    to the repository) and is published in out_dir (relative to the site)."""

    def fix(m):
        target = m.group(1)
        if "://" in target or target.startswith(("#", "mailto:")):
            return m.group(0)
        path, _, anchor = target.partition("#")
        anchor = "#" + anchor if anchor else ""
        repo_path = os.path.normpath(os.path.join(src_dir, path)).replace("\\", "/")
        if repo_path == "README.md":
            site_path = "index.md"
        elif repo_path == "docs/releases":
            site_path = "releases/index.md"
        elif repo_path.startswith("docs/"):
            site_path = repo_path[len("docs/"):]
        else:
            kind = "tree" if os.path.isdir(os.path.join(ROOT, repo_path)) else "blob"
            return f"]({REPO}/{kind}/main/{repo_path}{anchor})"
        rel = os.path.relpath(site_path, out_dir or ".").replace("\\", "/")
        return f"]({rel}{anchor})"

    return LINK.sub(fix, text)


def drop_nav_line(text):
    lines = text.split("\n")
    if lines and lines[0].startswith("[← Documentation"):
        lines = lines[1:]
        while lines and not lines[0].strip():
            lines = lines[1:]
    return "\n".join(lines)


def write(rel, text):
    path = os.path.join(OUT, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)


def tags():
    try:
        out = subprocess.run(["git", "tag", "-l", "v*"], cwd=ROOT, capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError):
        return set()
    return set(out.split())


def version_key(name):
    return [int(p) if p.isdigit() else p for p in re.split(r"[.\-]", name.lstrip("v").removesuffix(".md"))]


def main():
    shutil.rmtree(OUT, ignore_errors=True)
    shutil.copytree(os.path.join(ROOT, "docs", "assets"), os.path.join(OUT, "assets"),
                    ignore=shutil.ignore_patterns("*.py", "__pycache__"))

    with open(os.path.join(ROOT, "README.md"), encoding="utf-8") as f:
        write("index.md", rewrite(f.read(), ".", ""))

    for name in sorted(os.listdir(os.path.join(ROOT, "docs"))):
        if not name.endswith(".md"):
            continue
        with open(os.path.join(ROOT, "docs", name), encoding="utf-8") as f:
            write(name, rewrite(drop_nav_line(f.read()), "docs", ""))

    # Release notes: only versions that were tagged (the next one is a draft).
    released = tags()
    notes = [n for n in os.listdir(os.path.join(ROOT, "docs", "releases"))
             if n.endswith(".md") and n[:-3] in released]
    notes.sort(key=version_key, reverse=True)
    index = ["# Release notes", "",
             f"What each version brings. Downloads are on [GitHub Releases]({REPO}/releases).", ""]
    for n in notes:
        tag = n[:-3]
        with open(os.path.join(ROOT, "docs", "releases", n), encoding="utf-8") as f:
            body = f.read()
        write(f"releases/{n}", f"# grepalert {tag}\n\n" + rewrite(body, "docs/releases", "releases"))
        first = re.sub(r"\s+", " ", body.strip().split("\n\n")[0])
        index += [f"## [{tag}]({n})", "", first, ""]
    if not notes:
        index.append("No releases yet.")
    write("releases/index.md", "\n".join(index) + "\n")
    print(f"site sources in {OUT}: {len(os.listdir(OUT))} entries, release notes for {', '.join(n[:-3] for n in notes) or 'none'}")


if __name__ == "__main__":
    main()
