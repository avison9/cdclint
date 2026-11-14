# The quickstart recording in the README: docs/demo.gif and docs/demo.mp4.
# It downloads the real release and clones this repository, so it shows what
# a new user sees. Re-record it after a release changes the output or the
# version, from the repository root:
#
#   printf 'FROM ghcr.io/charmbracelet/vhs:v0.10.0\nRUN apt-get update --allow-releaseinfo-change && apt-get install -y --no-install-recommends git curl ca-certificates\n' | docker build -t cdclint-vhs -
#   docker run --rm --shm-size=2g -v "$PWD":/vhs -w /vhs cdclint-vhs docs/demo.tape
#
# --shm-size matters: without it Chromium drops about half the frames and
# the video plays at double speed. The safe.directory line is there because
# the container runs as root on files the host owns; for the same reason the
# new docs/demo.* come out owned by root (sudo chown them before committing).
Output "docs/demo.mp4"
Output "docs/demo.gif"

Set Shell "bash"
Set FontSize 18
Set Width 1280
Set Height 720
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 18ms
Set Framerate 24
Set WindowBar Colorful
Set CursorBlink false

Hide
Type "git config --global --add safe.directory '*' && mkdir -p /tmp/demo && cd /tmp/demo && export PS1='\[\e[1;34m\]$\[\e[0m\] ' && clear"
Enter
Sleep 500ms
Show

Type "# 1. Download a release and check it against the published checksums"
Enter
Type "curl -sSLO https://github.com/avison9/cdclint/releases/download/v0.4.0/cdclint_0.4.0_linux_amd64.tar.gz"
Enter
Sleep 3s
Type "curl -sSLO https://github.com/avison9/cdclint/releases/download/v0.4.0/checksums.txt"
Enter
Sleep 2s
Type "sha256sum --check --ignore-missing checksums.txt"
Enter
Sleep 2s
Type "tar -xzf cdclint_0.4.0_linux_amd64.tar.gz -C /usr/local/bin cdclint"
Enter
Sleep 1s
Type "cdclint --version"
Enter
Sleep 2s
Type "# (or: brew install avison9/tap/cdclint, or go install github.com/avison9/cdclint/cmd/cdclint@latest)"
Enter
Sleep 2.5s

Type "clear"
Enter
Type "# 2. Point it at your three schemas: migrations, connector, sink. Here, a real failure from the corpus:"
Enter
Type "git clone -q --depth 1 https://github.com/avison9/cdclint && cd cdclint"
Enter
Sleep 4s
Type "cdclint --migrations corpus/include-list-typo/migrations --connector corpus/include-list-typo/connector.json --sink corpus/include-list-typo/sink"
Enter
Sleep 1.5s
Type `echo "exit code: $?"`
Enter
Sleep 9s

Type "clear"
Enter
Type "# 3. In CI it fails the pull request: GitHub Action  uses: avison9/cdclint@v0"
Enter
Type "# Docs: github.com/avison9/cdclint"
Enter
Sleep 4s
