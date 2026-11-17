//go:build !darwin && !windows

package hostnet

import "context"

// Read returns nothing on this system: a Linux server takes its roots and proxy from the
// environment and the settings.
func Read(context.Context) (Settings, error) { return Settings{}, nil }
