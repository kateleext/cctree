#!/usr/bin/env bash
# Build release archives, publish a GitHub release and update the Homebrew tap.
# Usage: scripts/release.sh v0.1.0
set -euo pipefail

version=${1:?usage: scripts/release.sh vX.Y.Z}
repo=kateleext/cctree
tap=kateleext/homebrew-tap
root=$(cd "$(dirname "$0")/.." && pwd)
dist=$root/dist

rm -rf "$dist" && mkdir -p "$dist"
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  os=${target%/*} arch=${target#*/}
  dir=$dist/cctree_${os}_${arch}
  mkdir -p "$dir"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -C "$root" -trimpath \
    -ldflags "-s -w -X main.version=${version#v}" -o "$dir/cctree" .
  cp "$root/README.md" "$root/LICENSE" "$dir/"
  tar -C "$dir" -czf "$dist/cctree_${os}_${arch}.tar.gz" cctree README.md LICENSE
done

git -C "$root" tag -f "$version"
git -C "$root" push -f origin "$version"
gh release create "$version" --repo "$repo" --title "$version" --generate-notes "$dist"/*.tar.gz

sha() { sha256sum "$dist/cctree_$1.tar.gz" | cut -d' ' -f1; }
url=https://github.com/$repo/releases/download/$version
tapdir=$(mktemp -d)
gh repo clone "$tap" "$tapdir" -- -q
cat > "$tapdir/Formula/cctree.rb" <<RUBY
class Cctree < Formula
  desc "Browse every Claude Code session, running and past, as a folder tree"
  homepage "https://github.com/$repo"
  version "${version#v}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "$url/cctree_darwin_arm64.tar.gz"
      sha256 "$(sha darwin_arm64)"
    end
    if Hardware::CPU.intel?
      url "$url/cctree_darwin_amd64.tar.gz"
      sha256 "$(sha darwin_amd64)"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "$url/cctree_linux_arm64.tar.gz"
      sha256 "$(sha linux_arm64)"
    end
    if Hardware::CPU.intel? && Hardware::CPU.is_64_bit?
      url "$url/cctree_linux_amd64.tar.gz"
      sha256 "$(sha linux_amd64)"
    end
  end

  def install
    bin.install "cctree"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/cctree --version")
  end
end
RUBY
git -C "$tapdir" add Formula/cctree.rb
git -C "$tapdir" commit -qm "cctree $version"
git -C "$tapdir" push -q
echo "released $version; brew install kateleext/tap/cctree"
