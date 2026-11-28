// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"fmt"
	"testing"

	eventgrid "github.com/highfivelabs/aso-extensions/apis/arm/eventgrid/v20250215"
)

// The live group's partner configuration (docs/plan.md f77), the
// subscriber's side of Event Grid's partner events: a partner authorized
// by name, the default expiry changed, deleted. Free: a configuration is
// an authorization list and nothing is published. Not here: a partner
// registration, namespace and channel (the partner's side, which Microsoft
// onboards) and partner topics (a channel makes them).
func TestLiveEventGridPartnerConfiguration(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.EventGrid")
	ns := namespace(t, "egpartner")

	config := &eventgrid.PartnerConfiguration{}
	config.Namespace, config.Name = ns, "partners"
	config.Spec = eventgrid.PartnerConfiguration_Spec{Owner: owner("rg"), Location: ptr("global"),
		PartnerAuthorization: &eventgrid.PartnerAuthorization{DefaultMaximumExpirationTimeInDays: ptr(7),
			AuthorizedPartnersList: []eventgrid.Partner{{PartnerName: ptr("aso-extensions-live-" + runID)}}}}
	create(t, timeout, config)

	id := rgID() + "/providers/Microsoft.EventGrid/partnerConfigurations/default"
	update(t, config, func(c *eventgrid.PartnerConfiguration) {
		c.Spec.PartnerAuthorization.DefaultMaximumExpirationTimeInDays = ptr(14)
	})
	eventually(t, timeout, "the configuration Ready after the change", ready(config))
	expectResource(t, id, eventGridAPI, func(r map[string]any) error {
		auth, _ := properties(r)["partnerAuthorization"].(map[string]any)
		if fmt.Sprint(auth["defaultMaximumExpirationTimeInDays"]) != "14" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, config)
	expectGone(t, id, eventGridAPI)
}
