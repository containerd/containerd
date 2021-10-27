package files

// ToFile is an alias for n.(File). If the file isn't a regular file, nil value
// will be returned
func ToFile(n Node) File {
	f, _ := n.(File)
	return f
}

// ToDir is an alias for n.(Directory). If the file isn't directory, a nil value
// will be returned
func ToDir(n Node) Directory {
	d, _ := n.(Directory)
	return d
}

// FileFromEntry calls ToFile on Node in the given entry
func FileFromEntry(e DirEntry) File {
	return ToFile(e.Node())
}

// DirFromEntry calls ToDir on Node in the given entry
func DirFromEntry(e DirEntry) Directory {
	return ToDir(e.Node())
}
