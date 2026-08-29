package browser

import (
	"os"
	"strings"
)

// DefaultCamoufoxRelease is the default pinned version of Camoufox.
const DefaultCamoufoxRelease = "152.0.4-beta.29"

// CamoufoxRelease is an alias for DefaultCamoufoxRelease.
const CamoufoxRelease = DefaultCamoufoxRelease

// Release returns the configured or default Camoufox release version.
func Release() string {
	if env := strings.TrimSpace(os.Getenv("CAMOUFOX_VERSION")); env != "" {
		return env
	}
	return DefaultCamoufoxRelease
}
