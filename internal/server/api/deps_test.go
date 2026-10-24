// SPDX-License-Identifier: GPL-3.0-or-later

package api

import "testing"

func TestNewValidatesPackAndPlugins(t *testing.T) {
	env := newTestEnv(t, false)
	base := Deps{
		Config:   env.api.cfg,
		Logger:   env.api.logger,
		Dirs:     env.api.dirs,
		Store:    env.store,
		Security: env.security,
		Jobs:     env.jobs,
		App:      env.app,
		Pack:     env.pack,
		Plugins:  env.plugins,
	}
	if _, err := New(base); err != nil {
		t.Fatalf("New(valid deps) error = %v", err)
	}

	noPack := base
	noPack.Pack = nil
	if _, err := New(noPack); err == nil {
		t.Fatal("New without Pack succeeded, want an error")
	}

	noPlugins := base
	noPlugins.Plugins = nil
	if _, err := New(noPlugins); err == nil {
		t.Fatal("New without Plugins succeeded, want an error")
	}

	// Artifacts is optional scaffolding: nil simply leaves the download route
	// unmounted (production wiring always passes a store).
	noArtifacts := base
	noArtifacts.Artifacts = nil
	if _, err := New(noArtifacts); err != nil {
		t.Fatalf("New without Artifacts error = %v", err)
	}
}
