#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <version-or-tag> <checksums.txt> <output.rb>" >&2
  exit 2
fi

release_version=${1#v}
checksums_file=$2
output_file=$3

checksum_for() {
  asset_name=$1
  checksum=$(awk -v wanted="$asset_name" '$2 == wanted { print $1 }' "$checksums_file")
  if ! printf '%s' "$checksum" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "missing or invalid checksum for $asset_name" >&2
    exit 1
  fi
  printf '%s' "$checksum"
}

darwin_amd64=$(checksum_for "tailpreview_${release_version}_darwin_amd64.tar.gz")
darwin_arm64=$(checksum_for "tailpreview_${release_version}_darwin_arm64.tar.gz")
linux_amd64=$(checksum_for "tailpreview_${release_version}_linux_amd64.tar.gz")
linux_arm64=$(checksum_for "tailpreview_${release_version}_linux_arm64.tar.gz")

mkdir -p "$(dirname "$output_file")"

cat >"$output_file" <<EOF
# typed: strict
# frozen_string_literal: true

# This file is generated from Tailpreview release checksums. DO NOT EDIT.
class Tailpreview < Formula
  desc "Private HTTPS previews for localhost services over Tailscale Serve"
  homepage "https://github.com/JhumanJ/tailpreview"
  version "$release_version"
  license "MIT"

  depends_on "caddy"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/JhumanJ/tailpreview/releases/download/v#{version}/tailpreview_#{version}_darwin_arm64.tar.gz"
      sha256 "$darwin_arm64"
    else
      url "https://github.com/JhumanJ/tailpreview/releases/download/v#{version}/tailpreview_#{version}_darwin_amd64.tar.gz"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/JhumanJ/tailpreview/releases/download/v#{version}/tailpreview_#{version}_linux_arm64.tar.gz"
      sha256 "$linux_arm64"
    else
      url "https://github.com/JhumanJ/tailpreview/releases/download/v#{version}/tailpreview_#{version}_linux_amd64.tar.gz"
      sha256 "$linux_amd64"
    end
  end

  def install
    bin.install "tailpreview"
  end

  def caveats
    <<~EOS
      Tailscale must be installed, connected, and available on PATH.

      Verify the local prerequisites with:
        tailpreview doctor

      Optional automatic expiry cleanup:
        tailpreview service install
    EOS
  end

  test do
    assert_match "tailpreview #{version}", shell_output("#{bin}/tailpreview version")
  end
end
EOF
