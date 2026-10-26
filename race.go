//go:build race

package bbtree

// raceEnabled routes concurrent descents through Go code under the race
// detector. ConcurrentMap publishes copied routing nodes by replacing child
// pointers atomically. The assembly descents' plain child loads are atomic
// acquire loads on amd64 and dependency-ordered on arm64, but the detector
// cannot observe them; route's atomic loads give it the same synchronization
// to check.
const raceEnabled = true
