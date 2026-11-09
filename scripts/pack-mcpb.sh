#!/bin/sh
# Packs the Claude Desktop bundle (.mcpb) from the binaries GoReleaser has
# already built, without compiling anything again.
#
#   scripts/pack-mcpb.sh <dist dir> <version> <output file>
#
# GoReleaser calls it from the sboms section of .goreleaser.yaml, the only
# step of GoReleaser OSS that runs a command after the build and before the
# checksums. The output file is relative to the current directory, which
# GoReleaser sets to the dist dir.
#
# The bundle is one zip for every system: manifest.json with the version of
# the tag, icon.png, LICENSE, the launcher server/go-docs-mcp and one binary
# per target under server/<os>-<arch>/. It needs jq and zip, which the
# GitHub runners and Homebrew both have.
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 <dist dir> <version> <output file>" >&2
	exit 2
fi

dist_dir=$1
version=$2
output_file=$3

# The repository root, found from this script so the caller's directory does
# not matter.
repo_root=$(cd "$(dirname "$0")/.." && pwd)

# The targets of the builds section of .goreleaser.yaml. A target missing
# from dist stops the script, so a bundle never ships without one of them.
targets="darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64"

# The bundle is staged in dist, which is ignored by git and emptied by
# goreleaser --clean, so the release tree stays clean.
staging_dir="$dist_dir/mcpb"
rm -rf "$staging_dir"
mkdir -p "$staging_dir/server"

# The version in the repository is a placeholder; the one in the bundle is
# the one GoReleaser takes from the tag.
jq --arg version "$version" '.version = $version' \
	"$repo_root/mcpb/manifest.json" > "$staging_dir/manifest.json"

cp "$repo_root/mcpb/icon.png" "$staging_dir/icon.png"
cp "$repo_root/LICENSE" "$staging_dir/LICENSE"
cp "$repo_root/mcpb/server/go-docs-mcp" "$staging_dir/server/go-docs-mcp"
chmod 755 "$staging_dir/server/go-docs-mcp"

# GoReleaser writes each binary to dist/go-docs-mcp_<os>_<arch>_<level>/,
# where the level (v1, v8.0) depends on GOAMD64 and GOARM64. The glob takes
# whatever level was built and refuses to guess between two.
for target in $targets; do
	binary_name=go-docs-mcp
	case "$target" in
		windows_*) binary_name=go-docs-mcp.exe ;;
	esac

	found=""
	for candidate in "$dist_dir"/go-docs-mcp_"$target"*/"$binary_name"; do
		if [ ! -f "$candidate" ]; then
			continue
		fi
		if [ -n "$found" ]; then
			echo "pack-mcpb: more than one $target binary in $dist_dir" >&2
			exit 1
		fi
		found=$candidate
	done

	if [ -z "$found" ]; then
		echo "pack-mcpb: no $target binary in $dist_dir" >&2
		exit 1
	fi

	# server/darwin-arm64/ and the like, the folders the launcher and the
	# Windows entry of the manifest point at.
	target_dir="$staging_dir/server/$(echo "$target" | tr _ -)"
	mkdir -p "$target_dir"
	cp -p "$found" "$target_dir/$binary_name"
	chmod 755 "$target_dir/$binary_name"
done

# Every file gets the time of the binaries, which GoReleaser sets to the
# commit time and cp -p kept, and the zip lists the files in a fixed order
# with no extra attributes: two runs on the same tag give the same bundle.
reference_binary="$staging_dir/server/linux-amd64/go-docs-mcp"
find "$staging_dir" -exec touch -r "$reference_binary" {} +

rm -f "$output_file"
output_path=$(cd "$(dirname "$output_file")" && pwd)/$(basename "$output_file")
(
	cd "$staging_dir"
	find . -type f | LC_ALL=C sort | zip -q -X -9 "$output_path" -@
)

echo "pack-mcpb: wrote $output_file"
