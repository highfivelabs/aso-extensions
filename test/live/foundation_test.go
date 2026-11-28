// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	asoasg "github.com/Azure/azure-service-operator/v2/api/network/v1api20240101/storage"
	asowatcher "github.com/Azure/azure-service-operator/v2/api/network/v1api20241001/storage"
	asonetwork "github.com/Azure/azure-service-operator/v2/api/network/v20250301/storage"
	asoworkspace "github.com/Azure/azure-service-operator/v2/api/operationalinsights/v20250701/storage"

	network "github.com/highfivelabs/aso-extensions/apis/arm/network/v20260301"
	oi "github.com/highfivelabs/aso-extensions/apis/arm/operationalinsights/v20250701"
	sentinel "github.com/highfivelabs/aso-extensions/apis/arm/securityinsights/v20250901"
	sentinelpreview "github.com/highfivelabs/aso-extensions/apis/arm/securityinsights/v20251001preview"
)

// The kinds of docs/plan.md f29, f30 and f32 that fit the live resource
// group (written 2026-11-23, not yet run: docs/live-runs.md, "Pending").

const (
	sentinelAPI        = "2025-09-01"
	sentinelPreviewAPI = "2025-10-01-preview"
	fixtureNetworkAPI  = "2024-05-01"
)

// An application security group's address prefix set (free): created,
// changed, deleted; the group is a fixture standing in for ASO's
// ApplicationSecurityGroup.
func TestLiveAddressPrefixSet(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "asgprefix")
	asgID := rgID() + "/providers/Microsoft.Network/applicationSecurityGroups/" + azName("asg")
	fixture(t, asgID, fixtureNetworkAPI, map[string]any{"location": location})
	asg := &asoasg.ApplicationSecurityGroup{}
	asg.Namespace, asg.Name = ns, "web"
	asg.Spec.AzureName = azName("asg")
	asg.Spec.Location = ptr(location)
	asg.Spec.Owner = owner("rg")
	standIn(t, asg, asgID, nil)

	set := &network.ApplicationSecurityGroupsAddressPrefixSet{}
	set.Namespace, set.Name = ns, "partners"
	set.Spec = network.ApplicationSecurityGroupsAddressPrefixSet_Spec{
		AzureName: "partners", Owner: owner("web"),
		Properties: &network.AddressPrefixSetPropertiesFormat{AddressPrefixes: []string{"203.0.113.0/24"}},
	}
	create(t, timeout, set)
	id := asgID + "/addressPrefixSets/partners"
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		if !reflect.DeepEqual(properties(r)["addressPrefixes"], []any{"203.0.113.0/24"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, set, func(s *network.ApplicationSecurityGroupsAddressPrefixSet) {
		s.Spec.Properties.AddressPrefixes = []string{"203.0.113.0/24", "2001:db8::/32"}
	})
	eventually(t, timeout, "ApplicationSecurityGroupsAddressPrefixSet Ready after the change", ready(set))
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		if !reflect.DeepEqual(properties(r)["addressPrefixes"], []any{"203.0.113.0/24", "2001:db8::/32"}) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, set)
	expectGone(t, id, networkAPI)
}

// A rule collection group's draft on a firewall policy fixture (free while no
// firewall uses the policy): created, changed, deleted.
func TestLiveRuleCollectionGroupDraft(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "rcgdraft")
	policyID := rgID() + "/providers/Microsoft.Network/firewallPolicies/" + azName("fwp")
	fixture(t, policyID, fixtureNetworkAPI, map[string]any{"location": location, "properties": map[string]any{"sku": map[string]any{"tier": "Standard"}}})
	policy := &asonetwork.FirewallPolicy{}
	policy.Namespace, policy.Name = ns, "fwp"
	policy.Spec.AzureName = azName("fwp")
	policy.Spec.Location = ptr(location)
	policy.Spec.Owner = owner("rg")
	standIn(t, policy, policyID, nil)
	groupID := policyID + "/ruleCollectionGroups/apps"
	fixture(t, groupID, fixtureNetworkAPI, map[string]any{"properties": map[string]any{"priority": 300}})
	group := &asonetwork.FirewallPoliciesRuleCollectionGroup{}
	group.Namespace, group.Name = ns, "apps"
	group.Spec.AzureName = "apps"
	group.Spec.Owner = owner("fwp")
	group.Spec.Priority = ptr(300)
	standIn(t, group, groupID, nil)

	draft := &network.FirewallPoliciesRuleCollectionGroupDraft{}
	draft.Namespace, draft.Name = ns, "apps-draft"
	draft.Spec = network.FirewallPoliciesRuleCollectionGroupDraft_Spec{Owner: owner("apps"), Priority: ptr(310)}
	create(t, timeout, draft)
	id := groupID + "/ruleCollectionGroupDrafts/default"
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		if properties(r)["priority"] != float64(310) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, draft, func(d *network.FirewallPoliciesRuleCollectionGroupDraft) { d.Spec.Priority = ptr(320) })
	eventually(t, timeout, "FirewallPoliciesRuleCollectionGroupDraft Ready after the change", ready(draft))
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		if properties(r)["priority"] != float64(320) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, draft)
	expectGone(t, id, networkAPI)
}

// A Network Watcher connection monitor from a virtual network to an
// external address over TCP 443, written to a workspace (free with no agent
// running tests). A subscription has one network watcher per region, which
// Azure usually makes itself in NetworkWatcherRG, a group the live identity
// may not touch: the test makes its own in the live group and skips when
// Azure refuses a second one.
func TestLiveConnectionMonitor(t *testing.T) {
	t.Parallel()
	ns := namespace(t, "connmon")
	ctx := context.Background()
	watcherID := rgID() + "/providers/Microsoft.Network/networkWatchers/" + azName("nw")
	if _, _, err := azure.do(ctx, http.MethodPut, watcherID, fixtureNetworkAPI, map[string]any{"location": location}); err != nil {
		if strings.Contains(err.Error(), "NetworkWatcherCountLimitReached") {
			t.Skipf("the subscription has its network watcher for %s elsewhere (NetworkWatcherCountLimitReached): %v", location, err)
		}
		t.Fatalf("fixture network watcher: %v", err)
	}
	t.Cleanup(func() { deleteARM(t, watcherID, fixtureNetworkAPI) })
	watcher := &asowatcher.NetworkWatcher{}
	watcher.Namespace, watcher.Name = ns, "nw"
	watcher.Spec.AzureName = azName("nw")
	watcher.Spec.Location = ptr(location)
	watcher.Spec.Owner = owner("rg")
	standIn(t, watcher, watcherID, nil)
	vnetID := vnet(t, azName("cmnet"), "10.42.0.0/16", map[string]string{"default": "10.42.0.0/24"})
	wsID := logAnalyticsWorkspace(t, "cmlaw")

	monitor := &network.NetworkWatchersConnectionMonitor{}
	monitor.Namespace, monitor.Name = ns, "egress"
	monitor.Spec = network.NetworkWatchersConnectionMonitor_Spec{
		AzureName: azName("egress"), Owner: owner("nw"), Location: ptr(location),
		Endpoints: []network.ConnectionMonitorEndpoint{
			{Name: ptr("spoke"), Type: ptr(network.EndpointType_AzureVNet), ResourceReference: &genruntime.ResourceReference{ARMID: vnetID}},
			{Name: ptr("web"), Type: ptr(network.EndpointType_ExternalAddress), Address: ptr("www.microsoft.com")},
		},
		TestConfigurations: []network.ConnectionMonitorTestConfiguration{{
			Name: ptr("https"), Protocol: ptr(network.ConnectionMonitorTestConfigurationProtocol_Tcp), TestFrequencySec: ptr(60),
			TcpConfiguration: &network.ConnectionMonitorTcpConfiguration{Port: ptr(443)},
		}},
		TestGroups: []network.ConnectionMonitorTestGroup{{
			Name: ptr("egress"), Sources: []string{"spoke"}, Destinations: []string{"web"}, TestConfigurations: []string{"https"},
		}},
		Outputs: []network.ConnectionMonitorOutput{{
			Type:              ptr(network.OutputType_Workspace),
			WorkspaceSettings: &network.ConnectionMonitorWorkspaceSettings{WorkspaceResourceReference: &genruntime.ResourceReference{ARMID: wsID}},
		}},
	}
	create(t, timeout, monitor)
	id := watcherID + "/connectionMonitors/" + monitor.Spec.AzureName
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		p := properties(r)
		groups, _ := p["testGroups"].([]any)
		if len(groups) != 1 {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, monitor, func(m *network.NetworkWatchersConnectionMonitor) {
		m.Spec.TestConfigurations[0].TestFrequencySec = ptr(120)
	})
	eventually(t, timeout, "NetworkWatchersConnectionMonitor Ready after the change", ready(monitor))
	expectResource(t, id, networkAPI, func(r map[string]any) error {
		configs, _ := properties(r)["testConfigurations"].([]any)
		if len(configs) != 1 || configs[0].(map[string]any)["testFrequencySec"] != float64(120) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, monitor)
	expectGone(t, id, networkAPI)
}

// A Log Analytics summary rule over the workspace's own Usage table into a
// destination table Azure makes (free: Analytics tables, nothing ingested).
func TestLiveSummaryRule(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.OperationalInsights")
	ns := namespace(t, "summary")
	wsID := workspaceStandIn(t, ns)
	rule := &oi.WorkspacesSummaryLog{}
	rule.Namespace, rule.Name = ns, "usage-hourly"
	rule.Spec = oi.WorkspacesSummaryLog_Spec{
		AzureName: "usage-hourly", Owner: owner("law"), DisplayName: ptr("Usage by hour"), RuleType: ptr(oi.RuleTypeEnum_User),
		RuleDefinition: &oi.RuleDefinition{
			Query: ptr("Usage | summarize Quantity = sum(Quantity) by DataType"), BinSize: ptr(60), DestinationTable: ptr("UsageHourly_CL"),
		},
	}
	create(t, timeout, rule)
	id := wsID + "/summaryLogs/usage-hourly"
	expectResource(t, id, oiAPI, func(r map[string]any) error {
		def, _ := properties(r)["ruleDefinition"].(map[string]any)
		if def["binSize"] != float64(60) || def["destinationTable"] != "UsageHourly_CL" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, rule, func(s *oi.WorkspacesSummaryLog) { s.Spec.DisplayName = ptr("Usage per hour") })
	eventually(t, timeout, "WorkspacesSummaryLog Ready after the change", ready(rule))
	expectResource(t, id, oiAPI, func(r map[string]any) error {
		if properties(r)["displayName"] != "Usage per hour" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, rule)
	expectGone(t, id, oiAPI)
}

// Sentinel on two workspace fixtures (free with nothing ingested): the
// EyesOn setting, content metadata for an analytics rule fixture, and the
// workspace manager (configuration, a member workspace, a group of it and an
// assignment of the rule). Not here: SourceControl, which needs a real
// GitHub or Azure DevOps repository and a credential for it.
func TestLiveSentinelWorkspace(t *testing.T) {
	t.Parallel()
	requireProvider(t, "Microsoft.OperationalInsights")
	requireProvider(t, "Microsoft.SecurityInsights")
	ns := namespace(t, "sentinel")
	centralID := workspaceStandIn(t, ns)
	memberID := logAnalyticsWorkspace(t, "law-member-"+strings.TrimSuffix(ns, "-"+runID))
	member := &asoworkspace.Workspace{}
	member.Namespace, member.Name = ns, "member"
	member.Spec.AzureName = memberID[strings.LastIndex(memberID, "/")+1:]
	member.Spec.Owner = owner("rg")
	standIn(t, member, memberID, nil)
	for _, ws := range []string{centralID, memberID} {
		fixture(t, ws+"/providers/Microsoft.SecurityInsights/onboardingStates/default", sentinelAPI, map[string]any{"properties": map[string]any{}})
	}
	si := centralID + "/providers/Microsoft.SecurityInsights/"
	const ruleName = "4f0c6c1e-6a8e-4b8f-9a62-2c1b7d3e5a10"
	ruleID := si + "alertRules/" + ruleName
	fixture(t, ruleID, sentinelAPI, map[string]any{"kind": "Scheduled", "properties": map[string]any{
		"displayName": "live " + runID, "enabled": false, "severity": "Low", "query": "Usage | take 1",
		"queryFrequency": "PT1H", "queryPeriod": "PT1H", "triggerOperator": "GreaterThan", "triggerThreshold": 0,
		"suppressionDuration": "PT1H", "suppressionEnabled": false,
	}})
	law := &genruntime.ArbitraryOwnerReference{Group: "operationalinsights.azure.com", Kind: "Workspace", Name: "law"}

	setting := &sentinelpreview.Setting{}
	setting.Namespace, setting.Name = ns, "eyeson"
	setting.Spec = sentinelpreview.Setting_Spec{AzureName: "EyesOn", Owner: law, EyesOn: &sentinelpreview.EyesOn{Kind: ptr(sentinelpreview.EyesOn_Kind_EyesOn)}}
	metadata := &sentinel.ContentMetadata{}
	metadata.Namespace, metadata.Name = ns, "rule-metadata"
	metadata.Spec = sentinel.ContentMetadata_Spec{
		AzureName: "analyticsrule-" + ruleName, Owner: law, Kind: ptr("AnalyticsRule"), ContentId: ptr(ruleName), Version: ptr("1.0.0"),
		ParentReference: &genruntime.ResourceReference{ARMID: ruleID},
	}
	config := &sentinelpreview.WorkspaceManagerConfiguration{}
	config.Namespace, config.Name = ns, "manager"
	config.Spec = sentinelpreview.WorkspaceManagerConfiguration_Spec{AzureName: "default", Owner: law, Mode: ptr(sentinelpreview.Mode_Enabled)}
	create(t, timeout, setting, metadata, config)
	expectResource(t, si+"settings/EyesOn", sentinelPreviewAPI, func(r map[string]any) error {
		if r["kind"] != "EyesOn" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, si+"metadata/analyticsrule-"+ruleName, sentinelAPI, func(r map[string]any) error {
		if p := properties(r); p["kind"] != "AnalyticsRule" || !strings.EqualFold(fmt.Sprint(p["parentId"]), ruleID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, si+"workspaceManagerConfigurations/default", sentinelPreviewAPI, func(r map[string]any) error {
		if properties(r)["mode"] != "Enabled" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	memberObj := &sentinelpreview.WorkspaceManagerMember{}
	memberObj.Namespace, memberObj.Name = ns, "member"
	memberObj.Spec = sentinelpreview.WorkspaceManagerMember_Spec{
		AzureName: "member", Owner: law, TargetWorkspaceTenantId: ptr(os.Getenv("AZURE_TENANT_ID")),
		TargetWorkspaceResourceReference: &genruntime.ResourceReference{Group: "operationalinsights.azure.com", Kind: "Workspace", Name: "member"},
	}
	create(t, timeout, memberObj)
	group := &sentinelpreview.WorkspaceManagerGroup{}
	group.Namespace, group.Name = ns, "members"
	group.Spec = sentinelpreview.WorkspaceManagerGroup_Spec{AzureName: "members", Owner: law, DisplayName: ptr("Members"), MemberResourceNames: []string{"member"}}
	create(t, timeout, group)
	assignment := &sentinelpreview.WorkspaceManagerAssignment{}
	assignment.Namespace, assignment.Name = ns, "rules"
	assignment.Spec = sentinelpreview.WorkspaceManagerAssignment_Spec{
		AzureName: "rules", Owner: law, TargetResourceName: ptr("members"),
		Items: []sentinelpreview.AssignmentItem{{ResourceReference: &genruntime.ResourceReference{ARMID: ruleID}}},
	}
	create(t, timeout, assignment)
	expectResource(t, si+"workspaceManagerMembers/member", sentinelPreviewAPI, func(r map[string]any) error {
		if !strings.EqualFold(fmt.Sprint(properties(r)["targetWorkspaceResourceId"]), memberID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, si+"workspaceManagerAssignments/rules", sentinelPreviewAPI, func(r map[string]any) error {
		if properties(r)["targetResourceName"] != "members" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})

	// Deleted in the reverse order of what names what.
	for _, step := range []struct {
		obj        conditioned
		id, apiVer string
	}{
		{assignment, si + "workspaceManagerAssignments/rules", sentinelPreviewAPI},
		{group, si + "workspaceManagerGroups/members", sentinelPreviewAPI},
		{memberObj, si + "workspaceManagerMembers/member", sentinelPreviewAPI},
		{config, si + "workspaceManagerConfigurations/default", sentinelPreviewAPI},
		{metadata, si + "metadata/analyticsrule-" + ruleName, sentinelAPI},
		{setting, si + "settings/EyesOn", sentinelPreviewAPI},
	} {
		remove(t, timeout, step.obj)
		expectGone(t, step.id, step.apiVer)
	}
}
