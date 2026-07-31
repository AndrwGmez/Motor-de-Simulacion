package parser

import (
	"context"

	"github.com/flowverse/flowverse-api/internal/domain"
)

type Ambiguity struct {
	Code                string `json:"code"`
	Question            string `json:"question"`
	SuggestedResolution string `json:"suggestedResolution"`
}

type Result struct {
	Proposal    domain.FlowDefinition `json:"proposal"`
	Warnings    []string              `json:"warnings"`
	Ambiguities []Ambiguity           `json:"ambiguities"`
	Provider    string                `json:"provider"`
}

type FlowParser interface {
	Parse(context.Context, string) (Result, error)
}
