"""Writes the README for Docker Hub, which shows it outside the repository.

Usage: dockerhub_readme.py README.md output.md

Relative images and links point to the repository, and GitHub alerts ("> [!IMPORTANT]"),
which Docker Hub does not know, become a bold title.
"""
import re
import sys

REPOSITORY = "DeLFuS77/switch-library-manager-web"
RAW = f"https://raw.githubusercontent.com/{REPOSITORY}/master/"
BLOB = f"https://github.com/{REPOSITORY}/blob/master/"

source, target = sys.argv[1], sys.argv[2]
with open(source, encoding="utf-8") as f:
    text = f.read()


def relative(url):
    return not re.match(r"^([a-z]+:|#|/)", url)


# <img src="docs/...">
text = re.sub(r'(<img\b[^>]*\bsrc=")([^"]+)"', lambda m: m.group(1) + (RAW + m.group(2) if relative(m.group(2)) else m.group(2)) + '"', text)
# ![alt](docs/...) and [text](docs/...)
text = re.sub(r"(!?)\[([^\]]*)\]\(([^)\s]+)\)", lambda m: f"{m.group(1)}[{m.group(2)}]({(RAW if m.group(1) else BLOB) + m.group(3) if relative(m.group(3)) else m.group(3)})", text)
# > [!IMPORTANT] followed by the quoted text
text = re.sub(r"^> \[!(\w+)\]\n> ", lambda m: f"> **{m.group(1).capitalize()}:** ", text, flags=re.MULTILINE)

with open(target, "w", encoding="utf-8", newline="\n") as f:
    f.write(text)
