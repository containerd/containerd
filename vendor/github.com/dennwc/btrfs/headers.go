package btrfs

//go:generate go run ./cmd/hgen.go -u -g -t BTRFS_ -p btrfs -cs=treeKeyType:uint32=_KEY,objectID:uint64=_OBJECTID -cp=fileType=FT_,fileExtentType=FILE_EXTENT_,devReplaceItemState=DEV_REPLACE_ITEM_STATE_,blockGroup:uint64=BLOCK_GROUP_ -o btrfs_tree_hc.go btrfs_tree.h
//go:generate gofmt -l -w btrfs_tree_hc.go
