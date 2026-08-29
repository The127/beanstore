// Package lvm talks to lvm2 through its command line interface, the
// stable programmatic interface lvm2 offers since the removal of
// liblvm2app. Commands run through a Runner, so tests can substitute a
// fake and production code the real lvm binary.
package lvm
