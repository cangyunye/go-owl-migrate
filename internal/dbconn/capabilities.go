package dbconn

import (
	"sort"

	"github.com/cangyunye/owljdbc"
)

// TypeCapability reports the connectivity options one database type has in
// this deployment: whether the native driver is linked into the binary (and
// which build tag provides it when not), and whether the owljdbc agent
// channel covers the type with driver jars resolvable on disk.
type TypeCapability struct {
	Type      string `json:"type"`
	Native    bool   `json:"native"`               // native database/sql driver linked into this binary
	NativeTag string `json:"native_tag,omitempty"` // build tag that would link it (when known but unlinked)
	Agent     bool   `json:"agent"`                // owljdbc catalog covers the type
	JarPath   string `json:"jar_path,omitempty"`   // first matching driver jar ("" = none found)
}

// TypeCapabilities reports capabilities for every known database type,
// ordered by name. jarsDir is the agent jar search dir (global agent config).
// Pure presence probes: nothing is downloaded here — provisioning happens on
// the agent-channel open path (EnsureAgentJar).
func TypeCapabilities(jarsDir string) []TypeCapability {
	dirs := owljdbc.JarSearchDirs(jarsDir)
	seen := map[string]bool{}
	var types []string
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	for t := range knownTypes {
		add(t)
	}
	for _, t := range owljdbc.ProfileTypes() {
		add(t)
	}
	sort.Strings(types)

	out := make([]TypeCapability, 0, len(types))
	for _, t := range types {
		cap := TypeCapability{Type: t}
		if drv, ok := nativeDriverName(t); ok {
			cap.Native = DriverLinked(drv)
			if !cap.Native {
				cap.NativeTag = driverBuildTag[drv]
			}
		}
		if pt := agentProfileType(t); owljdbc.HasProfile(pt) {
			cap.Agent = true
			if jar, found := owljdbc.FindProfileJars(pt, dirs); found {
				cap.JarPath = jar
			}
		}
		out = append(out, cap)
	}
	return out
}
