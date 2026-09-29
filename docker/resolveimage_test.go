// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"context"
	"testing"
)

func TestResolveImageValid(t *testing.T) {
	valid := []ResolveImage{ResolveImageDefault, ResolveImageAlways, ResolveImageChanged, ResolveImageNever}
	for _, r := range valid {
		if !r.Valid() {
			t.Errorf("ResolveImage(%q).Valid() = false, want true", string(r))
		}
	}
	for _, r := range []ResolveImage{"latest", "Always", "digest"} {
		if r.Valid() {
			t.Errorf("ResolveImage(%q).Valid() = true, want false", string(r))
		}
	}
}

// An invalid mode must be refused before anything is written or deployed.
func TestDeployStackResolvedRejectsInvalidMode(t *testing.T) {
	err := DeployStackResolved(context.Background(), "stack", "services:\n  a:\n    image: nginx\n", "bogus", DeployOptions{})
	if err == nil {
		t.Fatal("DeployStackResolved with an invalid mode returned nil, want an error")
	}
	if got := err.Error(); got == "" || !contains(got, "--resolve-image") {
		t.Errorf("error = %q, want it to name --resolve-image", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
