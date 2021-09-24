// module empty-mod is an empty module that's used to help with containerd
// having a circular dependency on itself through plugin modules.
//
// We use this module as a "replace" rule in containerd's go.mod, to prevent
// relying on transitive dependencies (coming from older versions of containerd
// defined on plugin go.mod).
//
// The replace rule forces go modules to consider the "current" version of
// containerd to be the source of truth, helping us catch missing go.mod rules,
// or version changes early.
module empty-mod

go 1.13
