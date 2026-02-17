package erofs

type block struct {
	buf    []byte
	offset int32
	end    int32
}

func (b *block) bytes() []byte {
	if b.buf == nil || b.offset == -1 {
		return nil
	}
	return b.buf[b.offset:b.end]
}

func calculateBlocks(blockBits uint8, size int64) int {
	blockNum := size >> blockBits
	if size > blockNum<<blockBits {
		blockNum++
	}
	return int(blockNum)
}
