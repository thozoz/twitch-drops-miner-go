package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a formatted version string.
func String() string {
	return fmt.Sprintf("tdm %s (%s) built %s", Version, Commit, Date)
}
