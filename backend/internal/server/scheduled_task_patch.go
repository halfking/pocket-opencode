package server

import (
	"encoding/json"

	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

// scheduledTaskPatchInput keeps every writable field optional. The persisted
// TaskInput type is intentionally value-oriented for create; PATCH must not
// turn omitted values into defaults or clear fields accidentally.
type scheduledTaskPatchInput struct {
	Name         *string                     `json:"name"`
	Description  *string                     `json:"description"`
	Kind         *scheduledtask.Kind         `json:"kind"`
	ScheduleKind *scheduledtask.ScheduleKind `json:"scheduleKind"`
	ScheduleExpr *string                     `json:"scheduleExpr"`
	Timezone     *string                     `json:"timezone"`
	Payload      json.RawMessage             `json:"payload"`
	Enabled      *bool                       `json:"enabled"`
	MaxRuns      *int                        `json:"maxRuns"`
	CooldownSec  *int                        `json:"cooldownSec"`
	TimeoutSec   *int                        `json:"timeoutSec"`
}

func (p scheduledTaskPatchInput) merge(current *scheduledtask.Task) scheduledtask.TaskInput {
	input := scheduledtask.TaskInput{
		Name: current.Name, Description: current.Description, Kind: current.Kind,
		ScheduleKind: current.ScheduleKind, ScheduleExpr: current.ScheduleExpr,
		Timezone: current.Timezone, Payload: current.Payload,
		Enabled: boolPtr(current.Enabled), MaxRuns: current.MaxRuns,
		CooldownSec: current.CooldownSec, TimeoutSec: current.TimeoutSec,
	}
	if p.Name != nil {
		input.Name = *p.Name
	}
	if p.Description != nil {
		input.Description = *p.Description
	}
	if p.Kind != nil {
		input.Kind = *p.Kind
	}
	if p.ScheduleKind != nil {
		input.ScheduleKind = *p.ScheduleKind
	}
	if p.ScheduleExpr != nil {
		input.ScheduleExpr = *p.ScheduleExpr
	}
	if p.Timezone != nil {
		input.Timezone = *p.Timezone
	}
	if p.Payload != nil {
		input.Payload = p.Payload
	}
	if p.Enabled != nil {
		input.Enabled = p.Enabled
	}
	if p.MaxRuns != nil {
		input.MaxRuns = *p.MaxRuns
	}
	if p.CooldownSec != nil {
		input.CooldownSec = *p.CooldownSec
	}
	if p.TimeoutSec != nil {
		input.TimeoutSec = *p.TimeoutSec
	}
	return input
}

func boolPtr(v bool) *bool { return &v }
