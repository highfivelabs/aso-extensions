// Copyright 2026 High Five Labs
// SPDX-License-Identifier: Apache-2.0

//go:build live

package live

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"

	accessreview "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20211201preview"
	exemptions "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20260101preview"
	azpolicy "github.com/highfivelabs/aso-extensions/apis/arm/authorization/v20260701"
	costmanagement "github.com/highfivelabs/aso-extensions/apis/arm/costmanagement/v20260601"
	suppression "github.com/highfivelabs/aso-extensions/apis/arm/security/v20190101preview"
	governance "github.com/highfivelabs/aso-extensions/apis/arm/security/v20220101preview"
	defender "github.com/highfivelabs/aso-extensions/apis/arm/security/v20260701"
)

// The kinds kept at a subscription (docs/plan.md section 8, Q15, answered
// 2026-11-23: option b). Each test here runs only when hack/live/targets.txt
// names the subscription for it (subscriptionScope) and the identity holds
// the actions it needs there (requireSubscriptionAction: the custom roles of
// hack/live/roles/, which the builder assigns). Each deletes what it made and
// puts back any subscription setting it changed, read before the change.
// Written 2026-11-23, not yet run (docs/live-runs.md, "Pending").
// PIM (it could grant roles), the tenant's subscription policy and the
// management groups are not live-tested: fake only.

const (
	accessReviewAPI = "2021-12-01-preview"
	policyAPI       = "2026-07-01"
	exemptionAPI    = "2026-01-01-preview"
	costSettingAPI  = "2026-06-01"
	suppressionAPI  = "2019-01-01-preview"
	governanceAPI   = "2022-01-01-preview"
	pricingAPI      = "2026-07-01"
	detachOnDelete  = "detach-on-delete"
	reconcilePolicy = "serviceoperator.azure.com/reconcile-policy"
)

func subscriptionARMID() string { return "/subscriptions/" + subID }

func subscriptionOwner() *genruntime.ArbitraryOwnerReference {
	return &genruntime.ArbitraryOwnerReference{ARMID: subscriptionARMID()}
}

// subscriptionScope skips the test unless hack/live/targets.txt names the
// subscription and this test on one line: the subscription is a target for
// the tests named there only (a reviewed change).
func subscriptionScope(t *testing.T) {
	t.Helper()
	allowed, err := allowedTargets()
	if err != nil {
		t.Fatalf("reading hack/live/targets.txt: %v", err)
	}
	name := strings.SplitN(t.Name(), "/", 2)[0]
	if !slices.Contains(allowed[subID], name) {
		t.Skipf("hack/live/targets.txt does not name the subscription for %s: it may not touch the subscription", name)
	}
}

// requireSubscriptionAction skips the test unless the identity may take each
// action at the subscription (the roles of hack/live/roles/, assigned by the
// builder; docs/live-runs.md, "The subscription roles").
func requireSubscriptionAction(t *testing.T, actions ...string) {
	t.Helper()
	_, r, err := azure.get(context.Background(), subscriptionARMID()+"/providers/Microsoft.Authorization/permissions", "2022-04-01")
	if err != nil {
		t.Skipf("cannot read the identity's permissions at the subscription: %v", err)
	}
	for _, a := range actions {
		if !permitted(r, a) {
			t.Skipf("the identity may not %s at the subscription: the builder assigns hack/live/roles/subscription.json there", a)
		}
	}
}

// restoreAfter reads id now and, when the test ends (after the cleanups
// registered later, so after the objects are gone), puts back what it read:
// the same body with a PUT, or a DELETE when there was nothing. body turns the
// resource read into the PUT body; nil sends {"properties": ...}. The setting
// is then read back and compared.
func restoreAfter(t *testing.T, id, apiVersion string, body func(map[string]any) map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()
	code, before, err := azure.get(ctx, id, apiVersion)
	if code == http.StatusNotFound {
		before = nil
	} else if err != nil {
		t.Fatalf("reading %s before the change: %v", id, err)
	}
	if body == nil {
		body = func(r map[string]any) map[string]any { return map[string]any{"properties": properties(r)} }
	}
	t.Cleanup(func() {
		if before == nil {
			if code, _, err := azure.do(ctx, http.MethodDelete, id, apiVersion, nil); err != nil && code != http.StatusNotFound {
				t.Errorf("restoring %s (it was absent): %v", id, err)
			}
			return
		}
		if _, _, err := azure.do(ctx, http.MethodPut, id, apiVersion, body(before)); err != nil {
			t.Errorf("restoring %s to %v: %v", id, properties(before), err)
			return
		}
		eventually(t, timeout, "restored "+id, func() error {
			_, now, err := azure.get(ctx, id, apiVersion)
			if err != nil {
				return err
			}
			for k, v := range properties(body(before)) {
				if !reflect.DeepEqual(properties(now)[k], v) {
					return fmt.Errorf("%s is %v, was %v", k, properties(now)[k], v)
				}
			}
			return nil
		})
	})
	return before
}

// Access reviews at the subscription: a schedule definition that starts in a
// year (so no review runs and nobody is asked), changed and deleted; a history
// report definition, deleted; the subscription's default review settings,
// changed and put back as they were. Reviews of Azure resources need Entra ID
// P2 (or Governance) in the tenant; Azure's refusal names it.
func TestLiveAccessReviews(t *testing.T) {
	t.Parallel()
	subscriptionScope(t)
	requireSubscriptionAction(t,
		"Microsoft.Authorization/accessReviewScheduleDefinitions/write", "Microsoft.Authorization/accessReviewScheduleDefinitions/delete",
		"Microsoft.Authorization/accessReviewHistoryDefinitions/write", "Microsoft.Authorization/accessReviewHistoryDefinitions/delete",
		"Microsoft.Authorization/accessReviewScheduleSettings/write")
	ns := namespace(t, "accessreview")
	authz := subscriptionARMID() + "/providers/Microsoft.Authorization/"
	start := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02T00:00:00Z")

	t.Run("ScheduleDefinition", func(t *testing.T) {
		definition := &accessreview.AccessReviewScheduleDefinition{}
		definition.Namespace, definition.Name = ns, "quarterly"
		definition.Spec = accessreview.AccessReviewScheduleDefinition_Spec{
			Owner:                        subscriptionOwner(),
			DisplayName:                  ptr("aso-extensions live " + runID),
			DescriptionForAdmins:         ptr("made by an aso-extensions live test; deleted by it"),
			Reviewers:                    []accessreview.AccessReviewReviewer{{PrincipalId: ptr(identityObjectID(t))}},
			DefaultDecision:              ptr(accessreview.DefaultDecisionType_Approve),
			MailNotificationsEnabled:     ptr(false),
			ReminderNotificationsEnabled: ptr(false),
			InstanceDurationInDays:       ptr(14),
			Recurrence: &accessreview.AccessReviewRecurrenceSettings{
				PatternType: ptr(accessreview.AccessReviewRecurrencePatternType_AbsoluteMonthly),
				Interval:    ptr(3),
				RangeType:   ptr(accessreview.AccessReviewRecurrenceRangeType_NoEnd),
				StartDate:   ptr(start),
			},
		}
		create(t, timeout, definition)
		id := authz + "accessReviewScheduleDefinitions/" + definition.Spec.AzureName
		expectResource(t, id, accessReviewAPI, func(r map[string]any) error {
			p := properties(r)
			settings, _ := p["settings"].(map[string]any)
			if p["displayName"] != "aso-extensions live "+runID || settings["instanceDurationInDays"] != float64(14) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		update(t, definition, func(d *accessreview.AccessReviewScheduleDefinition) { d.Spec.InstanceDurationInDays = ptr(7) })
		eventually(t, timeout, "AccessReviewScheduleDefinition Ready after the change", ready(definition))
		expectResource(t, id, accessReviewAPI, func(r map[string]any) error {
			if settings, _ := properties(r)["settings"].(map[string]any); settings["instanceDurationInDays"] != float64(7) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, definition)
		expectGone(t, id, accessReviewAPI)
	})

	t.Run("HistoryDefinition", func(t *testing.T) {
		history := &accessreview.AccessReviewHistoryDefinition{}
		history.Namespace, history.Name = ns, "history"
		history.Spec = accessreview.AccessReviewHistoryDefinition_Spec{
			Owner:        subscriptionOwner(),
			OperatorSpec: &accessreview.AccessReviewHistoryDefinitionOperatorSpec{NamingConvention: ptr("random")},
			DisplayName:  ptr("aso-extensions live " + runID),
			Decisions:    []accessreview.AccessReviewResult{accessreview.AccessReviewResult_Approve, accessreview.AccessReviewResult_Deny},
		}
		create(t, timeout, history)
		id := authz + "accessReviewHistoryDefinitions/" + history.Spec.AzureName
		expectResource(t, id, accessReviewAPI, func(r map[string]any) error {
			if properties(r)["displayName"] != "aso-extensions live "+runID {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, history)
		expectGone(t, id, accessReviewAPI)
	})

	t.Run("ScheduleSetting", func(t *testing.T) {
		id := authz + "accessReviewScheduleSettings/default"
		// The PUT body is the settings themselves, not {"properties": ...}.
		before := restoreAfter(t, id, accessReviewAPI, func(r map[string]any) map[string]any { return properties(r) })
		if before == nil {
			t.Skip("Azure holds no default review settings at the subscription to put back")
		}
		was, _ := properties(before)["justificationRequiredOnApproval"].(bool)
		settings := &accessreview.AccessReviewScheduleSetting{}
		settings.Namespace, settings.Name = ns, "defaults"
		// A scope's settings cannot be deleted: the object is detached, and
		// restoreAfter puts back what Azure held.
		settings.Annotations = map[string]string{reconcilePolicy: detachOnDelete}
		settings.Spec = accessreview.AccessReviewScheduleSetting_Spec{Owner: subscriptionOwner(), JustificationRequiredOnApproval: ptr(!was)}
		create(t, timeout, settings)
		expectResource(t, id, accessReviewAPI, func(r map[string]any) error {
			if properties(r)["justificationRequiredOnApproval"] != !was {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
	})
}

// The subscription's Cost Management tag inheritance (cost data only, free):
// set, changed, deleted, then put back as it was (deleted again if it was
// not set).
func TestLiveCostTagInheritance(t *testing.T) {
	t.Parallel()
	subscriptionScope(t)
	requireSubscriptionAction(t, "Microsoft.CostManagement/settings/write", "Microsoft.CostManagement/settings/delete")
	ns := namespace(t, "costsetting")
	id := subscriptionARMID() + "/providers/Microsoft.CostManagement/settings/taginheritance"
	restoreAfter(t, id, costSettingAPI, func(r map[string]any) map[string]any {
		return map[string]any{"kind": "taginheritance", "properties": properties(r)}
	})
	setting := &costmanagement.Setting{}
	setting.Namespace, setting.Name = ns, "taginheritance"
	setting.Spec = costmanagement.Setting_Spec{
		Owner: subscriptionOwner(), Kind: ptr(costmanagement.SettingsKind_Taginheritance),
		Properties: &costmanagement.TagInheritanceProperties{PreferContainerTags: ptr(false)},
	}
	create(t, timeout, setting)
	expectResource(t, id, costSettingAPI, func(r map[string]any) error {
		if properties(r)["preferContainerTags"] != false {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, setting, func(s *costmanagement.Setting) { s.Spec.Properties.PreferContainerTags = ptr(true) })
	eventually(t, timeout, "Setting Ready after the change", ready(setting))
	expectResource(t, id, costSettingAPI, func(r map[string]any) error {
		if properties(r)["preferContainerTags"] != true {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	remove(t, timeout, setting)
	expectGone(t, id, costSettingAPI)
}

// Defender for Cloud rules at the subscription that change nothing while they
// exist: an alert suppression rule made Disabled (it suppresses no alert)
// and a governance rule made disabled; each changed, then deleted.
func TestLiveDefenderAlertRules(t *testing.T) {
	t.Parallel()
	subscriptionScope(t)
	requireSubscriptionAction(t,
		"Microsoft.Security/alertsSuppressionRules/write", "Microsoft.Security/alertsSuppressionRules/delete",
		"Microsoft.Security/governanceRules/write", "Microsoft.Security/governanceRules/delete")
	ns := namespace(t, "defenderrules")
	security := subscriptionARMID() + "/providers/Microsoft.Security/"

	t.Run("AlertsSuppressionRule", func(t *testing.T) {
		rule := &suppression.AlertsSuppressionRule{}
		rule.Namespace, rule.Name = ns, "ip-anomaly"
		rule.Spec = suppression.AlertsSuppressionRule_Spec{
			AzureName: azName("live"), Owner: subscriptionOwner(),
			AlertType: ptr("IpAnomaly"), Reason: ptr("FalsePositive"),
			Comment:           ptr("made by an aso-extensions live test; deleted by it"),
			State:             ptr(suppression.RuleState_Disabled),
			ExpirationDateUtc: ptr(time.Now().UTC().AddDate(0, 0, 7).Format(time.RFC3339)),
			SuppressionAlertsScope: &suppression.SuppressionAlertsScope{AllOf: []suppression.ScopeElement{{
				Field:                ptr("entities.ip.address"),
				AdditionalProperties: map[string]apiextensionsv1.JSON{"in": {Raw: []byte(`["203.0.113.7"]`)}},
			}}},
		}
		create(t, timeout, rule)
		id := security + "alertsSuppressionRules/" + rule.Spec.AzureName
		expectResource(t, id, suppressionAPI, func(r map[string]any) error {
			if p := properties(r); p["state"] != "Disabled" || p["alertType"] != "IpAnomaly" {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		update(t, rule, func(r *suppression.AlertsSuppressionRule) { r.Spec.Comment = ptr("changed") })
		eventually(t, timeout, "AlertsSuppressionRule Ready after the change", ready(rule))
		expectResource(t, id, suppressionAPI, func(r map[string]any) error {
			if properties(r)["comment"] != "changed" {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, rule)
		expectGone(t, id, suppressionAPI)
	})

	t.Run("GovernanceRule", func(t *testing.T) {
		rule := &governance.GovernanceRule{}
		rule.Namespace, rule.Name = ns, "critical"
		rule.Spec = governance.GovernanceRule_Spec{
			Owner:       subscriptionOwner(),
			DisplayName: ptr("aso-extensions live " + runID),
			Description: ptr("made by an aso-extensions live test; deleted by it"),
			IsDisabled:  ptr(true), IsGracePeriod: ptr(true),
			RulePriority: ptr(900), RemediationTimeframe: ptr("7.00:00:00"),
			RuleType: ptr(governance.GovernanceRuleType_Integrated), SourceResourceType: ptr(governance.GovernanceRuleSourceResourceType_Assessments),
			OwnerSource: &governance.GovernanceRuleOwnerSource{Type: ptr(governance.GovernanceRuleOwnerSourceType_Manually), Value: ptr("owner@example.com")},
			GovernanceEmailNotification: &governance.GovernanceRuleEmailNotification{
				DisableManagerEmailNotification: ptr(true), DisableOwnerEmailNotification: ptr(true),
			},
			ConditionSets: []apiextensionsv1.JSON{{Raw: []byte(`{"conditions":[{"property":"$.AssessmentKey","operator":"In","value":"[\"00000000-0000-0000-0000-000000000000\"]"}]}`)}},
		}
		create(t, timeout, rule)
		id := security + "governanceRules/" + rule.Spec.AzureName
		expectResource(t, id, governanceAPI, func(r map[string]any) error {
			if p := properties(r); p["isDisabled"] != true || p["rulePriority"] != float64(900) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		update(t, rule, func(r *governance.GovernanceRule) { r.Spec.RulePriority = ptr(901) })
		eventually(t, timeout, "GovernanceRule Ready after the change", ready(rule))
		expectResource(t, id, governanceAPI, func(r map[string]any) error {
			if properties(r)["rulePriority"] != float64(901) {
				return fmt.Errorf("resource: %v", r)
			}
			return nil
		})
		remove(t, timeout, rule)
		expectGone(t, id, governanceAPI)
	})
}

// The subscription's Defender for Key Vault plan, switched to the other tier
// and put back as it was. Key Vault is the plan chosen because it is billed
// per vault-month: the sandbox has no vault outside a live run's minutes. A
// plan cannot be deleted at a subscription: the object is detached.
func TestLiveDefenderPlan(t *testing.T) {
	t.Parallel()
	subscriptionScope(t)
	requireSubscriptionAction(t, "Microsoft.Security/pricings/write")
	ns := namespace(t, "defenderplan")
	id := subscriptionARMID() + "/providers/Microsoft.Security/pricings/KeyVaults"
	before := restoreAfter(t, id, pricingAPI, func(r map[string]any) map[string]any {
		p := map[string]any{"pricingTier": properties(r)["pricingTier"]}
		if sub, ok := properties(r)["subPlan"]; ok && p["pricingTier"] == "Standard" {
			p["subPlan"] = sub
		}
		return map[string]any{"properties": p}
	})
	if before == nil {
		t.Fatal("Azure has no KeyVaults plan at the subscription")
	}
	tier, subPlan := defender.PricingTier_Standard, ptr("PerKeyVault")
	if properties(before)["pricingTier"] == "Standard" {
		tier, subPlan = defender.PricingTier_Free, nil
	}
	plan := &defender.Pricing{}
	plan.Namespace, plan.Name = ns, "keyvaults"
	plan.Annotations = map[string]string{reconcilePolicy: detachOnDelete}
	plan.Spec = defender.Pricing_Spec{AzureName: "KeyVaults", Owner: subscriptionOwner(), PricingTier: ptr(tier), SubPlan: subPlan}
	create(t, timeout, plan)
	expectResource(t, id, pricingAPI, func(r map[string]any) error {
		if properties(r)["pricingTier"] != string(tier) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
}

// Azure Policy: a definition (audit only) and a set definition kept at the
// subscription, assigned only to the live resource group with enforcement
// off, an exemption there; the assignment changed; all deleted. The
// assignment and exemption need hack/live/roles/group-policy.json on the
// group (Contributor excludes Microsoft.Authorization writes).
func TestLiveAzurePolicy(t *testing.T) {
	t.Parallel()
	subscriptionScope(t)
	requireSubscriptionAction(t,
		"Microsoft.Authorization/policyDefinitions/write", "Microsoft.Authorization/policyDefinitions/delete",
		"Microsoft.Authorization/policySetDefinitions/write", "Microsoft.Authorization/policySetDefinitions/delete")
	requireAction(t, "Microsoft.Authorization/policyAssignments/write")
	requireAction(t, "Microsoft.Authorization/policyExemptions/write")
	ns := namespace(t, "policy")
	authz := "/providers/Microsoft.Authorization/"
	byObject := func(kind, name string) *genruntime.ResourceReference {
		return &genruntime.ResourceReference{Group: "authorization.azure.hfl.dev", Kind: kind, Name: name}
	}

	definition := &azpolicy.PolicyDefinition{}
	definition.Namespace, definition.Name = ns, "audit-ip-groups"
	definition.Spec = azpolicy.PolicyDefinition_Spec{
		AzureName: azName("audit-ip-groups"), Owner: subscriptionOwner(),
		PolicyType: ptr(azpolicy.PolicyType_Custom), Mode: ptr("Indexed"),
		DisplayName: ptr("aso-extensions live " + runID),
		PolicyRule:  &apiextensionsv1.JSON{Raw: []byte(`{"if":{"allOf":[{"field":"type","equals":"Microsoft.Network/ipGroups"},{"field":"tags['hfl-live-run']","exists":"false"}]},"then":{"effect":"audit"}}`)},
	}
	set := &azpolicy.PolicySetDefinition{}
	set.Namespace, set.Name = ns, "baseline"
	set.Spec = azpolicy.PolicySetDefinition_Spec{
		AzureName: azName("baseline"), Owner: subscriptionOwner(), PolicyType: ptr(azpolicy.PolicyType_Custom),
		DisplayName: ptr("aso-extensions live " + runID),
		PolicyDefinitions: []azpolicy.PolicyDefinitionReference{{
			PolicyDefinitionReference: byObject("PolicyDefinition", "audit-ip-groups"), PolicyDefinitionReferenceId: ptr("audit-ip-groups"),
		}},
	}
	assignment := &azpolicy.PolicyAssignment{}
	assignment.Namespace, assignment.Name = ns, "baseline-rg"
	assignment.Spec = azpolicy.PolicyAssignment_Spec{
		AzureName: azName("baseline"), Owner: &genruntime.ArbitraryOwnerReference{ARMID: rgID()},
		PolicyDefinitionReference: byObject("PolicySetDefinition", "baseline"),
		EnforcementMode:           ptr(azpolicy.PolicyAssignmentProperties_EnforcementMode_DoNotEnforce),
		DisplayName:               ptr("aso-extensions live " + runID),
	}
	exemption := &exemptions.PolicyExemption{}
	exemption.Namespace, exemption.Name = ns, "waiver"
	exemption.Spec = exemptions.PolicyExemption_Spec{
		AzureName: azName("waiver"), Owner: &genruntime.ArbitraryOwnerReference{ARMID: rgID()},
		PolicyAssignmentReference: byObject("PolicyAssignment", "baseline-rg"),
		ExemptionCategory:         ptr(exemptions.ExemptionCategory_Waiver),
		ExpiresOn:                 ptr(time.Now().UTC().AddDate(0, 0, 7).Format(time.RFC3339)),
		Description:               ptr("made by an aso-extensions live test; deleted by it"),
	}
	// Created in order and deleted in reverse: the exemption, the
	// assignment, the set, the definition.
	create(t, timeout, definition, set, assignment, exemption)

	definitionID := subscriptionARMID() + authz + "policyDefinitions/" + definition.Spec.AzureName
	setID := subscriptionARMID() + authz + "policySetDefinitions/" + set.Spec.AzureName
	assignmentID := rgID() + authz + "policyAssignments/" + assignment.Spec.AzureName
	exemptionID := rgID() + authz + "policyExemptions/" + exemption.Spec.AzureName
	expectResource(t, definitionID, policyAPI, func(r map[string]any) error {
		if properties(r)["mode"] != "Indexed" || properties(r)["policyType"] != "Custom" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, setID, policyAPI, func(r map[string]any) error {
		defs, _ := properties(r)["policyDefinitions"].([]any)
		if len(defs) != 1 || !strings.EqualFold(fmt.Sprint(defs[0].(map[string]any)["policyDefinitionId"]), definitionID) {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, assignmentID, policyAPI, func(r map[string]any) error {
		p := properties(r)
		if !strings.EqualFold(fmt.Sprint(p["policyDefinitionId"]), setID) || p["enforcementMode"] != "DoNotEnforce" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	expectResource(t, exemptionID, exemptionAPI, func(r map[string]any) error {
		p := properties(r)
		if !strings.EqualFold(fmt.Sprint(p["policyAssignmentId"]), assignmentID) || p["exemptionCategory"] != "Waiver" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	update(t, assignment, func(a *azpolicy.PolicyAssignment) { a.Spec.Description = ptr("changed") })
	eventually(t, timeout, "PolicyAssignment Ready after the change", ready(assignment))
	expectResource(t, assignmentID, policyAPI, func(r map[string]any) error {
		if properties(r)["description"] != "changed" {
			return fmt.Errorf("resource: %v", r)
		}
		return nil
	})
	for _, o := range []conditioned{exemption, assignment, set, definition} {
		remove(t, timeout, o)
	}
	expectGone(t, exemptionID, exemptionAPI)
	expectGone(t, assignmentID, policyAPI)
	expectGone(t, setID, policyAPI)
	expectGone(t, definitionID, policyAPI)
}
