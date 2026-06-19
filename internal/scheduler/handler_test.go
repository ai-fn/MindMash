package scheduler

import (
	"testing"

	schedulerv1 "github.com/mindmesh/mindmesh/api/scheduler/v1"
)

func TestCardStateToProto(t *testing.T) {
	tests := []struct {
		domain CardState
		want   schedulerv1.CardState
	}{
		{StateNew, schedulerv1.CardState_CARD_STATE_NEW},
		{StateLearning, schedulerv1.CardState_CARD_STATE_LEARNING},
		{StateReview, schedulerv1.CardState_CARD_STATE_REVIEW},
		{StateRelearning, schedulerv1.CardState_CARD_STATE_RELEARNING},
	}
	for _, tt := range tests {
		if got := cardStateToProto(tt.domain); got != tt.want {
			t.Errorf("cardStateToProto(%v) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestGradeFromProto(t *testing.T) {
	tests := []struct {
		proto schedulerv1.Grade
		want  Grade
	}{
		{schedulerv1.Grade_GRADE_AGAIN, Again},
		{schedulerv1.Grade_GRADE_HARD, Hard},
		{schedulerv1.Grade_GRADE_GOOD, Good},
		{schedulerv1.Grade_GRADE_EASY, Easy},
	}
	for _, tt := range tests {
		if got := gradeFromProto(tt.proto); got != tt.want {
			t.Errorf("gradeFromProto(%v) = %v, want %v", tt.proto, got, tt.want)
		}
	}
}

func TestRescheduleActionFromProto(t *testing.T) {
	tests := []struct {
		proto schedulerv1.RescheduleAction
		want  RescheduleAction
	}{
		{schedulerv1.RescheduleAction_RESCHEDULE_ACTION_SUSPEND, ActionSuspend},
		{schedulerv1.RescheduleAction_RESCHEDULE_ACTION_BURY, ActionBury},
		{schedulerv1.RescheduleAction_RESCHEDULE_ACTION_RESET, ActionReset},
	}
	for _, tt := range tests {
		if got := rescheduleActionFromProto(tt.proto); got != tt.want {
			t.Errorf("rescheduleActionFromProto(%v) = %v, want %v", tt.proto, got, tt.want)
		}
	}
}
