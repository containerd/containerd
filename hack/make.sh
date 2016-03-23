#!/usr/bin/env bash
set -e
set -o pipefail

export CONTAINER_PKG='github.com/docker/containerd'
export SCRIPTDIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
export MAKEDIR="$SCRIPTDIR/make"

# We're a nice, sexy, little shell script, and people might try to run us;
# but really, they shouldn't. We want to be in a container!
if [ "$PWD" != "/go/src/$CONTAINER_PKG" ]; then
	{
		echo "# WARNING! I don't seem to be running in the Docker container."
		echo "# The result of this command might be an incorrect build, and will not be"
		echo "#   officially supported."
		echo "#"
		echo "# Try this instead: make all"
		echo "#"
	} >&2
fi

echo

# List of bundles to create when no argument is passed
DEFAULT_BUNDLES=(
	containerd
	containerd-shim
	ctr

	validate
	test
	vet
	lint
)

VERSION=$(< ./VERSION)
if command -v git &> /dev/null && git rev-parse &> /dev/null; then
	GITCOMMIT=$(git rev-parse --short HEAD)
	if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
		GITCOMMIT="$GITCOMMIT-dirty"
	fi
	BUILDTIME=$(date -u)
else
	echo >&2 'error: .git directory missing and DOCKER_GITCOMMIT not specified'
	echo >&2 '  Please either build with the .git directory accessible, or specify the'
	echo >&2 '  exact (--short) commit hash you are building using DOCKER_GITCOMMIT for'
	echo >&2 '  future accountability in diagnosing build issues.  Thanks!'
	exit 1
fi

if [ ! "$GOPATH" ]; then
	echo >&2 'error: missing GOPATH; please see https://golang.org/doc/code.html#GOPATH'
	exit 1
fi

# ORIG_BUILDFLAGS is necessary for the cross target which cannot always build
# with options like -race.
ORIG_BUILDFLAGS=( -a -tags "netgo static_build sqlite_omit_load_extension $DOCKER_BUILDTAGS" -installsuffix netgo )
# see https://github.com/golang/go/issues/9369#issuecomment-69864440 for why -installsuffix is necessary here
BUILDFLAGS=( $BUILDFLAGS "${ORIG_BUILDFLAGS[@]}" )

# a helper to provide ".exe" when it's appropriate
binary_extension() {
	if [ "$(go env GOOS)" = 'windows' ]; then
		echo -n '.exe'
	fi
}

hash_files() {
	while [ $# -gt 0 ]; do
		f="$1"
		shift
		dir="$(dirname "$f")"
		base="$(basename "$f")"
		for hashAlgo in md5 sha256; do
			if command -v "${hashAlgo}sum" &> /dev/null; then
				(
					# subshell and cd so that we get output files like:
					#   $HASH docker-$VERSION
					# instead of:
					#   $HASH /go/src/github.com/.../$VERSION/binary/docker-$VERSION
					cd "$dir"
					"${hashAlgo}sum" "$base" > "$base.$hashAlgo"
				)
			fi
		done
	done
}

bundle() {
	local bundle="$1"; shift
	echo "---> Making bundle: $(basename "$bundle") (in $DEST)"
	source "$SCRIPTDIR/make/$bundle" "$@"
}

main() {
	# We want this to fail if the bundles already exist and cannot be removed.
	# This is to avoid mixing bundles from different versions of the code.
	if [ "$(go env GOHOSTOS)" != 'windows' ]; then
		# Windows and symlinks don't get along well

		rm -f bin/latest
		ln -s "$VERSION" bin/latest
	fi

	if [ $# -lt 1 ]; then
		bundles=(${DEFAULT_BUNDLES[@]})
	else
		bundles=($@)
	fi
	for bundle in ${bundles[@]}; do
		export DEST="bin/$VERSION/$(basename "$bundle")"
		# Cygdrive paths don't play well with go build -o.
		if [[ "$(uname -s)" == CYGWIN* ]]; then
			export DEST="$(cygpath -mw "$DEST")"
		fi
		mkdir -p "$DEST"
		ABS_DEST="$(cd "$DEST" && pwd -P)"
		bundle "$bundle"
		echo
	done
}

main "$@"
