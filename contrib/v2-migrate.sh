#!/bin/sh
set -e
for GOFILE in $(find . -name "*.go" | grep -v "./vendor/" ); do
  #First migrate containerd imports to v2 module
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_\.a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd(?!\/v2)(\/\S+)?"/$1$2"github.com\/containerd\/containerd\/v2$3"/g' $GOFILE

  #Migrate moved packages
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_\.a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2"/$1$2"github.com\/containerd\/containerd\/v2\/client"/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/content\/local/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/content\/local/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/content/$1$2"github.com\/containerd\/containerd\/v2\/core\/content/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/containers/$1$2"github.com\/containerd\/containerd\/v2\/core\/containers/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/diff\/lcow/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/diff\/lcow/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/diff\/walking/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/diff\/walking/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/diff\/windows/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/diff\/windows/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/diff/$1$2"github.com\/containerd\/containerd\/v2\/core\/diff/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/images/$1$2"github.com\/containerd\/containerd\/v2\/core\/images/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/leases\/plugin/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/leases/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/leases/$1$2"github.com\/containerd\/containerd\/v2\/core\/leases/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/metadata\/plugin/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/metadata/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/metadata/$1$2"github.com\/containerd\/containerd\/v2\/core\/metadata/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/mount/$1$2"github.com\/containerd\/containerd\/v2\/core\/mount/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/remotes/$1$2"github.com\/containerd\/containerd\/v2\/core\/remotes/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/runtime\/restart\/monitor/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/restart/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/sandbox/$1$2"github.com\/containerd\/containerd\/v2\/core\/sandbox/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/services\/server/$1$2"github.com\/containerd\/containerd\/v2\/cmd\/containerd\/server/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/services/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/services/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/blockfile/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/blockfile/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/btrfs/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/btrfs/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/devmapper/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/devmapper/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/lcow/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/lcow/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/native/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/native/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/overlay/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/overlay/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots\/windows/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/snapshots\/windows/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/snapshots/$1$2"github.com\/containerd\/containerd\/v2\/core\/snapshots/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/archive/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/archive/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/cio/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/cio/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/events/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/events/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/errdefs/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/errdefs/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/filters/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/filters/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/gc\/scheduler/$1$2"github.com\/containerd\/containerd\/v2\/plugins\/gc/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/gc/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/gc/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/identifiers/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/identifiers/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/labels/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/labels/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/namespaces/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/namespaces/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/oci/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/oci/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/reference/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/reference/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/rootfs/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/rootfs/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/sys/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/sys/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/tracing/$1$2"github.com\/containerd\/containerd\/v2\/pkg\/tracing/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/cleanup/$1$2"github.com\/containerd\/containerd\/v2\/internal\/cleanup/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/failpoint/$1$2"github.com\/containerd\/containerd\/v2\/internal\/failpoint/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/hasher/$1$2"github.com\/containerd\/containerd\/v2\/internal\/hasher/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/kmutex/$1$2"github.com\/containerd\/containerd\/v2\/internal\/kmutex/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/randutil/$1$2"github.com\/containerd\/containerd\/v2\/internal\/randutil/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/registrar/$1$2"github.com\/containerd\/containerd\/v2\/internal\/registrar/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/testutil/$1$2"github.com\/containerd\/containerd\/v2\/internal\/testutil/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/tomlext/$1$2"github.com\/containerd\/containerd\/v2\/internal\/tomlext/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/pkg\/truncindex/$1$2"github.com\/containerd\/containerd\/v2\/internal\/truncindex/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/metrics/$1$2"github.com\/containerd\/containerd\/v2\/core\/metrics/g' $GOFILE
  perl -pi -e 's/([\t]|[ ]{2,8}|import )([_a-zA-Z0-9]+ )?"github\.com\/containerd\/containerd\/v2\/runtime/$1$2"github.com\/containerd\/containerd\/v2\/core\/runtime/g' $GOFILE
done
