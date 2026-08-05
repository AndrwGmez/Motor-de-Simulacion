package enterprise

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxPolicyPatterns  = 32
	maxPatternLength   = 256
	maxPolicyValueSize = 512
	maxWildcardTokens  = 8
)

type PolicyEffect string

const (
	PolicyAllow PolicyEffect = "allow"
	PolicyDeny  PolicyEffect = "deny"
)

func (effect PolicyEffect) Valid() bool {
	return effect == PolicyAllow || effect == PolicyDeny
}

type PolicyConditions struct {
	Roles []OrganizationRole `json:"roles,omitempty"`
}

type PolicyRule struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organizationId"`
	Description    string           `json:"description,omitempty"`
	Effect         PolicyEffect     `json:"effect"`
	Actions        []string         `json:"actions"`
	Resources      []string         `json:"resources"`
	Conditions     PolicyConditions `json:"conditions"`
	Disabled       bool             `json:"disabled"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
}

func (rule PolicyRule) Normalize() PolicyRule {
	rule.Description = strings.TrimSpace(rule.Description)
	rule.Actions = canonicalStrings(rule.Actions, false)
	rule.Resources = canonicalStrings(rule.Resources, false)
	roles := make([]string, 0, len(rule.Conditions.Roles))
	for _, role := range rule.Conditions.Roles {
		roles = append(roles, string(role))
	}
	roles = canonicalStrings(roles, false)
	rule.Conditions.Roles = make([]OrganizationRole, 0, len(roles))
	for _, role := range roles {
		rule.Conditions.Roles = append(rule.Conditions.Roles, OrganizationRole(role))
	}
	return rule
}

func (rule PolicyRule) Validate() error {
	if err := validateUUID("id", rule.ID, false); err != nil {
		return err
	}
	if err := validateUUID("organizationId", rule.OrganizationID, false); err != nil {
		return err
	}
	if !rule.Effect.Valid() {
		return invalid("effect", "must be allow or deny")
	}
	if rule.Description != strings.TrimSpace(rule.Description) || len([]rune(rule.Description)) > 500 {
		return invalid("description", "must be trimmed and contain at most 500 characters")
	}
	if len(rule.Actions) == 0 || len(rule.Actions) > maxPolicyPatterns {
		return invalid("actions", fmt.Sprintf("must contain between 1 and %d patterns", maxPolicyPatterns))
	}
	if len(rule.Resources) == 0 || len(rule.Resources) > maxPolicyPatterns {
		return invalid("resources", fmt.Sprintf("must contain between 1 and %d patterns", maxPolicyPatterns))
	}
	if !equalStrings(rule.Actions, canonicalStrings(rule.Actions, false)) {
		return invalid("actions", "must be trimmed, unique, and sorted")
	}
	if !equalStrings(rule.Resources, canonicalStrings(rule.Resources, false)) {
		return invalid("resources", "must be trimmed, unique, and sorted")
	}
	for index, pattern := range rule.Actions {
		if err := validateGlobPattern(pattern); err != nil {
			return invalid(fmt.Sprintf("actions[%d]", index), err.Error())
		}
	}
	for index, pattern := range rule.Resources {
		if err := validateGlobPattern(pattern); err != nil {
			return invalid(fmt.Sprintf("resources[%d]", index), err.Error())
		}
	}
	if len(rule.Conditions.Roles) > 4 {
		return invalid("conditions.roles", "contains too many roles")
	}
	roleNames := make([]string, 0, len(rule.Conditions.Roles))
	for index, role := range rule.Conditions.Roles {
		if !role.Valid() {
			return invalid(fmt.Sprintf("conditions.roles[%d]", index), "contains an invalid organization role")
		}
		roleNames = append(roleNames, string(role))
	}
	if !equalStrings(roleNames, canonicalStrings(roleNames, false)) {
		return invalid("conditions.roles", "must be unique and sorted")
	}
	return validateTimestamps(rule.CreatedAt, rule.UpdatedAt)
}

type PolicyRequest struct {
	OrganizationID string           `json:"organizationId"`
	SubjectID      string           `json:"subjectId"`
	Role           OrganizationRole `json:"role"`
	Action         string           `json:"action"`
	Resource       string           `json:"resource"`
}

func (request PolicyRequest) Validate() error {
	if err := validateUUID("organizationId", request.OrganizationID, false); err != nil {
		return err
	}
	if err := validateUUID("subjectId", request.SubjectID, false); err != nil {
		return err
	}
	if !request.Role.Valid() {
		return invalid("role", "must be a valid organization role")
	}
	if err := validatePolicyLiteral(request.Action); err != nil {
		return invalid("action", err.Error())
	}
	if err := validatePolicyLiteral(request.Resource); err != nil {
		return invalid("resource", err.Error())
	}
	return nil
}

type PolicyDecisionReason string

const (
	DecisionExplicitAllow PolicyDecisionReason = "explicit_allow"
	DecisionExplicitDeny  PolicyDecisionReason = "explicit_deny"
	DecisionNoMatch       PolicyDecisionReason = "no_matching_rule"
)

type PolicyDecision struct {
	Allowed        bool                 `json:"allowed"`
	Effect         PolicyEffect         `json:"effect"`
	Reason         PolicyDecisionReason `json:"reason"`
	MatchedRuleIDs []string             `json:"matchedRuleIds"`
}

// PolicyEngine is immutable after construction and therefore safe for
// concurrent evaluations. Rules are normalized and sorted by ID so decisions
// and their explanations do not depend on input or map iteration order.
type PolicyEngine struct {
	rules []PolicyRule
}

func NewPolicyEngine(rules []PolicyRule) (*PolicyEngine, error) {
	normalized := make([]PolicyRule, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		rule = rule.Normalize()
		if err := rule.Validate(); err != nil {
			return nil, fmt.Errorf("policy rule %d: %w", index, err)
		}
		if _, exists := seen[rule.ID]; exists {
			return nil, invalid("id", "policy rule IDs must be unique")
		}
		seen[rule.ID] = struct{}{}
		normalized[index] = clonePolicyRule(rule)
	}
	sort.Slice(normalized, func(left, right int) bool { return normalized[left].ID < normalized[right].ID })
	return &PolicyEngine{rules: normalized}, nil
}

func (engine *PolicyEngine) Evaluate(request PolicyRequest) (PolicyDecision, error) {
	if engine == nil {
		return PolicyDecision{}, invalid("engine", "is required")
	}
	if err := request.Validate(); err != nil {
		return PolicyDecision{}, err
	}

	matched := make([]string, 0)
	hasAllow := false
	hasDeny := false
	for _, rule := range engine.rules {
		if rule.Disabled || rule.OrganizationID != request.OrganizationID || !roleMatches(rule.Conditions.Roles, request.Role) {
			continue
		}
		if !matchesAny(rule.Actions, request.Action) || !matchesAny(rule.Resources, request.Resource) {
			continue
		}
		matched = append(matched, rule.ID)
		if rule.Effect == PolicyDeny {
			hasDeny = true
		} else {
			hasAllow = true
		}
	}

	decision := PolicyDecision{
		Effect:         PolicyDeny,
		Reason:         DecisionNoMatch,
		MatchedRuleIDs: matched,
	}
	if hasDeny {
		decision.Reason = DecisionExplicitDeny
		return decision, nil
	}
	if hasAllow {
		decision.Allowed = true
		decision.Effect = PolicyAllow
		decision.Reason = DecisionExplicitAllow
	}
	return decision, nil
}

func (engine *PolicyEngine) Rules() []PolicyRule {
	if engine == nil {
		return []PolicyRule{}
	}
	result := make([]PolicyRule, len(engine.rules))
	for index, rule := range engine.rules {
		result[index] = clonePolicyRule(rule)
	}
	return result
}

func clonePolicyRule(rule PolicyRule) PolicyRule {
	rule.Actions = append([]string(nil), rule.Actions...)
	rule.Resources = append([]string(nil), rule.Resources...)
	rule.Conditions.Roles = append([]OrganizationRole(nil), rule.Conditions.Roles...)
	return rule
}

func roleMatches(allowed []OrganizationRole, actual OrganizationRole) bool {
	if len(allowed) == 0 {
		return true
	}
	index := sort.Search(len(allowed), func(index int) bool { return allowed[index] >= actual })
	return index < len(allowed) && allowed[index] == actual
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if safeGlobMatch(pattern, value) {
			return true
		}
	}
	return false
}

// safeGlobMatch supports only literals, a segment wildcard (*) that cannot
// cross ':' or '/', and a global wildcard (**). Dynamic programming and strict
// input bounds avoid regular-expression injection and backtracking blowups.
func safeGlobMatch(pattern, value string) bool {
	previous := make([]bool, len(value)+1)
	previous[0] = true
	for patternIndex := 0; patternIndex < len(pattern); {
		current := make([]bool, len(value)+1)
		if pattern[patternIndex] == '*' {
			global := patternIndex+1 < len(pattern) && pattern[patternIndex+1] == '*'
			if global {
				patternIndex += 2
			} else {
				patternIndex++
			}
			current[0] = previous[0]
			for valueIndex := 1; valueIndex <= len(value); valueIndex++ {
				canConsume := global || (value[valueIndex-1] != ':' && value[valueIndex-1] != '/')
				current[valueIndex] = previous[valueIndex] || (canConsume && current[valueIndex-1])
			}
		} else {
			literal := pattern[patternIndex]
			patternIndex++
			for valueIndex := 1; valueIndex <= len(value); valueIndex++ {
				current[valueIndex] = previous[valueIndex-1] && value[valueIndex-1] == literal
			}
		}
		previous = current
	}
	return previous[len(value)]
}

func validateGlobPattern(pattern string) error {
	if pattern == "" || len(pattern) > maxPatternLength || !utf8.ValidString(pattern) {
		return fmt.Errorf("must contain between 1 and %d ASCII characters", maxPatternLength)
	}
	wildcardTokens := 0
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		if character >= utf8.RuneSelf || !safePolicyCharacter(character, true) {
			return fmt.Errorf("contains an unsupported character")
		}
		if character == '*' {
			if index == 0 || pattern[index-1] != '*' {
				wildcardTokens++
			}
			if index >= 2 && pattern[index-1] == '*' && pattern[index-2] == '*' {
				return fmt.Errorf("wildcards may contain at most two consecutive asterisks")
			}
		}
	}
	if wildcardTokens > maxWildcardTokens {
		return fmt.Errorf("contains too many wildcard tokens")
	}
	if hasTraversalSegment(pattern) {
		return fmt.Errorf("must not contain traversal segments")
	}
	return nil
}

func validatePolicyLiteral(value string) error {
	if value == "" || len(value) > maxPolicyValueSize || !utf8.ValidString(value) {
		return fmt.Errorf("must contain between 1 and %d ASCII characters", maxPolicyValueSize)
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= utf8.RuneSelf || !safePolicyCharacter(character, false) {
			return fmt.Errorf("contains an unsupported character")
		}
	}
	if hasTraversalSegment(value) {
		return fmt.Errorf("must not contain traversal segments")
	}
	return nil
}

func safePolicyCharacter(character byte, allowWildcard bool) bool {
	if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
		return true
	}
	switch character {
	case '.', '_', ':', '/', '@', '-':
		return true
	case '*':
		return allowWildcard
	default:
		return false
	}
}

func hasTraversalSegment(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
