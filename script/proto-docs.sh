#!/usr/bin/env bash
set -eu -o pipefail

mkdir -p docs/grpc

DOC_URL_PATH="${DOC_URL_PATH-$PWD}"
target=docs/grpc
templates=docs/grpc-templates
filemap="$templates/filemap.xml"

cp "$templates/index-header.html" "$target/index.html"


for pkg in $(find api -name '*.proto' -printf "%h\n" | sort | uniq); do
    protoc \
        --doc_out="root=$DOC_URL_PATH,filemap=$filemap:$target" \
        --proto_path=/go/src/ \
        --proto_path=./protobuf/ \
        --proto_path=./vendor/github.com/gogo/protobuf/ \
        $PWD/$pkg/*.proto
    cat "$target/index-item.html" >> "$target/index.html"
done

rm -f "$target/index-item.html"
cat "$templates/index-footer.html" >> "$target/index.html"
