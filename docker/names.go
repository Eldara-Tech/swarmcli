// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"fmt"
	"regexp"
)

// MaxSwarmObjectNameLen is the length swarmkit's
// validateConfigOrSecretAnnotations allows a secret or config name. Services
// and networks are held to a stricter 63 by a different validator, so this is
// not a limit for swarm objects in general.
const MaxSwarmObjectNameLen = 64

// swarmObjectNameRe is swarmkit's own expression for a secret or config name.
var swarmObjectNameRe = regexp.MustCompile(`^[a-zA-Z0-9]+(?:[a-zA-Z0-9-_.]*[a-zA-Z0-9])?$`)

// ValidateSwarmObjectName reports why the daemon would refuse this name for a
// secret or a config, so the dialog asking for it can say so rather than
// letting a create round-trip and come back as a raw daemon error. kind names
// the object in the message ("secret", "config").
func ValidateSwarmObjectName(kind, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s name cannot be empty", kind)
	case len(name) > MaxSwarmObjectNameLen:
		return fmt.Errorf("%s name must be %d characters or fewer (this one is %d)", kind, MaxSwarmObjectNameLen, len(name))
	case !swarmObjectNameRe.MatchString(name):
		return fmt.Errorf("%s name may contain only letters, digits, '-', '_' and '.', and must start and end with a letter or a digit", kind)
	}
	return nil
}
