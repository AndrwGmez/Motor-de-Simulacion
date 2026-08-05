package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/enterprise"
)

type policyRuleRequest struct {
	Description string                       `json:"description,omitempty"`
	Effect      enterprise.PolicyEffect      `json:"effect"`
	Actions     []string                     `json:"actions"`
	Resources   []string                     `json:"resources"`
	Conditions  *enterprise.PolicyConditions `json:"conditions"`
	Disabled    *bool                        `json:"disabled"`
}

func (s *Server) listPolicyRules(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	rules, err := repository.ListPolicyRules(c.Request.Context(), access.Organization.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if rules == nil {
		rules = []enterprise.PolicyRule{}
	}
	c.JSON(http.StatusOK, gin.H{"items": rules})
}

func (s *Server) getPolicyRule(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	ruleID, ok := canonicalUUIDParam(c, "ruleId")
	if !ok {
		return
	}
	rule, err := repository.GetPolicyRule(c.Request.Context(), access.Organization.ID, ruleID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (s *Server) createPolicyRule(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	var request policyRuleRequest
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	if !validatePolicyRuleRequest(c, request) {
		return
	}
	timestamp := now()
	rule := policyRuleFromRequest(request)
	rule.ID, rule.OrganizationID = uuid.NewString(), access.Organization.ID
	rule.CreatedAt, rule.UpdatedAt = timestamp, timestamp
	rule = rule.Normalize()
	if err := rule.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.policy.create", "policy_rule", rule.ID,
		map[string]any{"effect": rule.Effect, "disabled": rule.Disabled})
	if err := repository.SavePolicyRule(ctx, rule); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, rule)
}

func (s *Server) updatePolicyRule(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	ruleID, ok := canonicalUUIDParam(c, "ruleId")
	if !ok {
		return
	}
	existing, err := repository.GetPolicyRule(c.Request.Context(), access.Organization.ID, ruleID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	var request policyRuleRequest
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	if !validatePolicyRuleRequest(c, request) {
		return
	}
	rule := policyRuleFromRequest(request)
	rule.ID, rule.OrganizationID = existing.ID, existing.OrganizationID
	rule.CreatedAt, rule.UpdatedAt = existing.CreatedAt, now()
	rule = rule.Normalize()
	if err := rule.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.policy.update", "policy_rule", rule.ID,
		map[string]any{"effect": rule.Effect, "disabled": rule.Disabled})
	if err := repository.SavePolicyRule(ctx, rule); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (s *Server) deletePolicyRule(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	ruleID, ok := canonicalUUIDParam(c, "ruleId")
	if !ok {
		return
	}
	rule, err := repository.GetPolicyRule(c.Request.Context(), access.Organization.ID, ruleID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.policy.delete", "policy_rule", rule.ID,
		map[string]any{"effect": rule.Effect})
	if err := repository.DeletePolicyRule(ctx, access.Organization.ID, rule.ID); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) evaluateOrganizationPolicy(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository)
	if !ok {
		return
	}
	var request struct {
		Action   string `json:"action"`
		Resource string `json:"resource"`
	}
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	policyRequest := enterprise.PolicyRequest{
		OrganizationID: access.Organization.ID,
		SubjectID:      currentUser(c).ID,
		Role:           access.Membership.Role,
		Action:         request.Action,
		Resource:       request.Resource,
	}
	if err := policyRequest.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	rules, err := repository.ListPolicyRules(c.Request.Context(), access.Organization.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	engine, err := enterprise.NewPolicyEngine(rules)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "policy.invalid_configuration", "Stored policy configuration is invalid", nil)
		return
	}
	decision, err := engine.Evaluate(policyRequest)
	if err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	outcome := enterprise.AuditDenied
	if decision.Allowed {
		outcome = enterprise.AuditSucceeded
	}
	if _, audited := s.appendEnterpriseAudit(c, repository, access.Organization.ID,
		"organization.policy.evaluate", "policy_resource", request.Resource, outcome,
		map[string]any{
			"requestedAction": request.Action,
			"allowed":         decision.Allowed,
			"reason":          decision.Reason,
			"matchedRuleIds":  decision.MatchedRuleIDs,
		}); !audited {
		return
	}
	c.JSON(http.StatusOK, decision)
}

func policyRuleFromRequest(request policyRuleRequest) enterprise.PolicyRule {
	return enterprise.PolicyRule{
		Description: request.Description, Effect: request.Effect,
		Actions: request.Actions, Resources: request.Resources,
		Conditions: *request.Conditions, Disabled: *request.Disabled,
	}
}

func validatePolicyRuleRequest(c *gin.Context, request policyRuleRequest) bool {
	if request.Conditions == nil {
		writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", gin.H{"field": "conditions", "reason": "is required"})
		return false
	}
	if request.Disabled == nil {
		writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", gin.H{"field": "disabled", "reason": "is required"})
		return false
	}
	return true
}
