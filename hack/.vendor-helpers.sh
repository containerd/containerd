#!/usr/bin/env bash

PROJECT=github.com/docker/containerd

# Downloads dependencies into vendor/ directory
mkdir -p vendor

export GOPATH="$GOPATH:${PWD}/vendor"

find='find'
if [ "$(go env GOHOSTOS)" = 'windows' ]; then
	find='/usr/bin/find'
fi

clone() {
	local vcs="$1"
	local pkg="$2"
	local rev="$3"
	local url="$4"

	: ${url:=https://$pkg}
	local target="vendor/$pkg"

	echo -n "$pkg @ $rev: "

	if [ -d "$target" ]; then
		echo -n 'rm old, '
		rm -rf "$target"
	fi

	echo -n 'clone, '
	case "$vcs" in
		git)
			git clone --quiet --no-checkout "$url" "$target"
			( cd "$target" && git checkout --quiet "$rev" && git reset --quiet --hard "$rev" )
			;;
		hg)
			hg clone --quiet --updaterev "$rev" "$url" "$target"
			;;
	esac

	echo -n 'rm VCS, '
	( cd "$target" && rm -rf .{git,hg} )

	echo -n 'rm vendor, '
	( cd "$target" && rm -rf vendor Godeps/_workspace )

	echo done
}

clean() {
	local packages=(
		"${PROJECT}/containerd" # package main
		"${PROJECT}/ctr" # package main
		"${PROJECT}/containerd-shim" # package main
		"${PROJECT}/integration-test" # package main
	)
	local platforms=( linux/amd64 linux/386 windows/amd64 windows/386 darwin/amd64 )
	local buildTagCombos=(
		'libcontainer runc seccomp'
	)

	echo

	echo -n 'collecting import graph, '
	local IFS=$'\n'
	local imports=( $(
		for platform in "${platforms[@]}"; do
			export GOOS="${platform%/*}";
			export GOARCH="${platform##*/}";
			for buildTags in "${buildTagCombos[@]}"; do
				go list -e -tags "$buildTags" -f '{{join .Deps "\n"}}' "${packages[@]}"
				go list -e -tags "$buildTags" -f '{{join .TestImports "\n"}}' "${packages[@]}"
			done
		done | grep -vE "^${PROJECT}" | sort -u
	) )
	imports=( $(go list -e -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "${imports[@]}") )
	unset IFS

	echo -n 'pruning unused packages, '
	findArgs=(
		# for some reason go list doesn't detect this as a dependency
		-path vendor/github.com/vdemeester/shakers
	)
	for import in "${imports[@]}"; do
		[ "${#findArgs[@]}" -eq 0 ] || findArgs+=( -or )
		findArgs+=( -path "vendor/$import" )
	done
	local IFS=$'\n'
	local prune=( $($find vendor -depth -type d -not '(' "${findArgs[@]}" ')') )
	unset IFS
	for dir in "${prune[@]}"; do
		$find "$dir" -maxdepth 1 -not -type d -not -name 'LICENSE*' -not -name 'COPYING*' -exec rm -v -f '{}' ';'
		rmdir "$dir" 2>/dev/null || true
	done

	echo -n 'pruning unused files, '
	$find vendor -type f -name '*_test.go' -exec rm -v '{}' ';'

	echo done
}

# Fix up hard-coded imports that refer to Godeps paths so they'll work with our vendoring
fix_rewritten_imports () {
       local pkg="$1"
       local remove="${pkg}/Godeps/_workspace/src/"
       local target="vendor/$pkg"

       echo "$pkg: fixing rewritten imports"
       $find "$target" -name \*.go -exec sed -i -e "s|\"${remove}|\"|g" {} \;
}
