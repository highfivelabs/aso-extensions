// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

//go:build fakecloud

// Test builds only (-tags fakecloud): --fake-cloud sends every Entra request
// the credentials make to the in-process fake Entra (pkg/fakecloud/entra),
// so the manager runs against the fake cloud with azidentity's credentials
// unchanged. The Key Vault and PostgreSQL fakes are reached over the
// network; their CA comes in through SSL_CERT_FILE, as any CA would.
package main

import (
	"flag"

	"github.com/highfivelabs/aso-extensions/pkg/fakecloud/entra"
	"github.com/highfivelabs/aso-extensions/pkg/manager"
)

var fakeCloud = flag.Bool("fake-cloud", false, "tests only: use the in-process fake Entra for every credential")

func init() {
	testOptions = func(o *manager.Options) {
		if *fakeCloud {
			o.TokenProvider = entra.TokenProvider(&entra.Transport{})
		}
	}
}
