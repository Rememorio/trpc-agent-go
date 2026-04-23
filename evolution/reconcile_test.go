//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSkillName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"Foo Workflow", "foo workflow"},
		{"  Foo   Bar\tBaz\n", "foo bar baz"},
		{"FOO-WORKFLOW", "foo-workflow"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, normalizeSkillName(c.in), "input=%q", c.in)
	}
}

func TestQuantifiedFamilyKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"weather monitor - 3 cities", "weather monitor - <count:city>"},
		{"weather monitor - multi-city", "weather monitor - <count:city>"},
		{"recipe cookbook - multiple dishes with apis", "recipe cookbook - <count:dish> with apis"},
		{"economic snapshot", "economic snapshot"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, quantifiedFamilyKey(c.in), "input=%q", c.in)
	}
}

func TestIsTaskVariantSeparator(t *testing.T) {
	yes := []string{
		" - 3 cities", "- variant", " (3 items)", "(scope)",
		" [variant]", "[scope]", " : variant", ": variant", " /scale",
		"/scale", " |tag", "|tag", "_suffix", ".v2",
		" v2", " V10", "v3", "V99",
	}
	no := []string{
		"", " name", "abc", "vfoo", " vfoo", "  ", "a", "Ext",
	}
	for _, s := range yes {
		assert.True(t, isTaskVariantSeparator(s), "expected separator for %q", s)
	}
	for _, s := range no {
		assert.False(t, isTaskVariantSeparator(s), "expected NO separator for %q", s)
	}
}

func TestReconcileWithLibrary_NilDecisionIsNoOp(t *testing.T) {
	got, events := reconcileWithLibrary(nil, []ExistingSkill{{Name: "Foo"}})
	assert.Nil(t, got)
	assert.Empty(t, events)
}

func TestReconcileWithLibrary_NoExistingSkillsLeavesSkillsAlone(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name: "Whatever - X", Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	out, events := reconcileWithLibrary(in, nil)
	require.NotNil(t, out)
	require.Len(t, out.Skills, 1)
	assert.Empty(t, out.Updates)
	assert.Empty(t, events)
}

func TestReconcileWithLibrary_RewritesStrictSupersetToUpdate(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow - 3 Cities",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	existing := []ExistingSkill{{Name: "Foo Workflow"}}

	out, events := reconcileWithLibrary(in, existing)
	require.Len(t, out.Skills, 0, "candidate should be rewritten, not retained as a new skill")
	require.Len(t, out.Updates, 1)
	upd := out.Updates[0]
	assert.Equal(t, "Foo Workflow", upd.Name)
	require.NotNil(t, upd.NewSpec)
	assert.Equal(t, "Foo Workflow", upd.NewSpec.Name,
		"NewSpec name must be aligned with the parent so the on-disk dir does not move")
	require.Len(t, events, 1)
	assert.Equal(t, reconcileRewriteToUpdate, events[0].Kind)
	assert.Equal(t, "Foo Workflow - 3 Cities", events[0].Original)
	assert.Equal(t, "Foo Workflow", events[0].Target)
}

func TestReconcileWithLibrary_RewritesExactNameMatchToUpdate(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	existing := []ExistingSkill{{Name: "Foo Workflow"}}

	out, events := reconcileWithLibrary(in, existing)
	require.Empty(t, out.Skills)
	require.Len(t, out.Updates, 1)
	assert.Equal(t, "Foo Workflow", out.Updates[0].Name)
	require.Len(t, events, 1)
	assert.Equal(t, reconcileRewriteToUpdate, events[0].Kind)
}

func TestReconcileWithLibrary_PicksLongestMatchingParent(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow - Multi-City - 3 Cities",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	existing := []ExistingSkill{
		{Name: "Foo Workflow"},
		{Name: "Foo Workflow - Multi-City"},
	}

	out, events := reconcileWithLibrary(in, existing)
	require.Empty(t, out.Skills)
	require.Len(t, out.Updates, 1)
	assert.Equal(t, "Foo Workflow - Multi-City", out.Updates[0].Name,
		"longest matching parent must win")
	require.Len(t, events, 1)
	assert.Equal(t, "Foo Workflow - Multi-City", events[0].Target)
}

func TestReconcileWithLibrary_KeepsCandidateWhenNoSeparator(t *testing.T) {
	// "Foo Workflow Extended" shares a prefix with "Foo Workflow" but the
	// next char is a letter, not a separator. Don't rewrite.
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow Extended",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	existing := []ExistingSkill{{Name: "Foo Workflow"}}

	out, events := reconcileWithLibrary(in, existing)
	require.Len(t, out.Skills, 1)
	assert.Empty(t, out.Updates)
	assert.Empty(t, events)
}

func TestReconcileWithLibrary_DropsCandidateWhenReviewerAlreadyUpdatesParent(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow - 3 Cities",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
		Updates: []*SkillUpdate{{
			Name: "Foo Workflow",
			NewSpec: &SkillSpec{
				Name: "Foo Workflow", Description: "explicit",
				WhenToUse: "w", Steps: []string{"s"},
			},
		}},
	}
	existing := []ExistingSkill{{Name: "Foo Workflow"}}

	out, events := reconcileWithLibrary(in, existing)
	require.Empty(t, out.Skills, "candidate should be dropped; reviewer already covered the parent")
	require.Len(t, out.Updates, 1, "explicit update must survive untouched")
	assert.Equal(t, "explicit", out.Updates[0].NewSpec.Description)
	require.Len(t, events, 1)
	assert.Equal(t, reconcileDropConflictsWithExistingUpdate, events[0].Kind)
}

func TestReconcileWithLibrary_DoesNotRewriteWhenParentIsBeingDeleted(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow - 3 Cities",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
		Deletions: []string{"Foo Workflow"},
	}
	existing := []ExistingSkill{{Name: "Foo Workflow"}}

	out, events := reconcileWithLibrary(in, existing)
	require.Len(t, out.Skills, 1, "delete-then-add is the reviewer's prerogative; do not rewrite")
	assert.Empty(t, out.Updates)
	assert.Empty(t, events)
}

func TestReconcileWithLibrary_CollapsesMultipleCandidatesToSameParent(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{
			{Name: "Foo Workflow - 3 Cities", Description: "d", WhenToUse: "w", Steps: []string{"a"}},
			{Name: "Foo Workflow - 4 Cities", Description: "d", WhenToUse: "w", Steps: []string{"b"}},
			{Name: "Foo Workflow (5 cities)", Description: "d", WhenToUse: "w", Steps: []string{"c"}},
		},
	}
	existing := []ExistingSkill{{Name: "Foo Workflow"}}

	out, events := reconcileWithLibrary(in, existing)
	assert.Empty(t, out.Skills)
	require.Len(t, out.Updates, 1, "only the first candidate per parent is kept")
	assert.Equal(t, "Foo Workflow", out.Updates[0].Name)
	// First → rewritten, two follow-ups → dropped as intra-batch duplicates of the same target.
	require.Len(t, events, 3)
	assert.Equal(t, reconcileRewriteToUpdate, events[0].Kind)
	assert.Equal(t, reconcileDropIntraBatchDuplicate, events[1].Kind)
	assert.Equal(t, reconcileDropIntraBatchDuplicate, events[2].Kind)
}

func TestReconcileWithLibrary_RewritesQuantifiedSiblingToGenericParent(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Weather Monitor - 3 Cities with Historical Data",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	existing := []ExistingSkill{
		{Name: "Weather Monitor - Multi-City with Historical Data"},
	}

	out, events := reconcileWithLibrary(in, existing)
	require.Empty(t, out.Skills)
	require.Len(t, out.Updates, 1)
	assert.Equal(t, "Weather Monitor - Multi-City with Historical Data", out.Updates[0].Name)
	require.Len(t, events, 1)
	assert.Equal(t, reconcileRewriteQuantifiedSiblingToUpdate, events[0].Kind)
	assert.Equal(t, "Weather Monitor - Multi-City with Historical Data", events[0].Target)
}

func TestReconcileWithLibrary_DoesNotGuessCanonicalSiblingWithoutGenericParent(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Weather Monitor - 3 Cities",
			Description: "d", WhenToUse: "w", Steps: []string{"s"},
		}},
	}
	existing := []ExistingSkill{
		{Name: "Weather Monitor - 4 Cities"},
		{Name: "Weather Monitor - 5 Cities"},
	}

	out, events := reconcileWithLibrary(in, existing)
	require.Len(t, out.Skills, 1)
	assert.Empty(t, out.Updates)
	assert.Empty(t, events)
}

func TestReconcileWithLibrary_DedupsIntraBatchByName(t *testing.T) {
	in := &ReviewDecision{
		Skills: []*SkillSpec{
			{Name: "Brand New", Description: "first", WhenToUse: "w1", Steps: []string{"s"}},
			{Name: "brand  new", Description: "second", WhenToUse: "w2", Steps: []string{"t"}},
		},
	}
	out, events := reconcileWithLibrary(in, nil)
	require.Len(t, out.Skills, 1, "name-collapse should drop the second entry")
	assert.Equal(t, "first", out.Skills[0].Description, "first occurrence must win")
	require.Len(t, events, 1)
	assert.Equal(t, reconcileDropIntraBatchDuplicate, events[0].Kind)
}

func TestReconcileWithLibrary_DedupsIntraBatchByShape(t *testing.T) {
	steps := []string{"call api", "save json"}
	in := &ReviewDecision{
		Skills: []*SkillSpec{
			{Name: "Alpha", Description: "first", WhenToUse: "do thing", Steps: steps},
			{Name: "Beta", Description: "second", WhenToUse: "DO THING", Steps: []string{"Call API", "Save JSON"}},
		},
	}
	out, events := reconcileWithLibrary(in, nil)
	require.Len(t, out.Skills, 1, "shape-collapse should drop the second entry")
	assert.Equal(t, "Alpha", out.Skills[0].Name)
	require.Len(t, events, 1)
	assert.Equal(t, reconcileDropIntraBatchDuplicate, events[0].Kind)
}

func TestReconcileWithLibrary_EmptyShapeKeyDoesNotForceDedup(t *testing.T) {
	// Two candidates with empty when_to_use should still be considered
	// "different" by the shape rule (empty key is ignored). Here neither
	// has any other reason to dedup.
	in := &ReviewDecision{
		Skills: []*SkillSpec{
			{Name: "Alpha", Description: "a", WhenToUse: "", Steps: nil},
			{Name: "Beta", Description: "b", WhenToUse: "", Steps: nil},
		},
	}
	out, events := reconcileWithLibrary(in, nil)
	assert.Len(t, out.Skills, 2)
	assert.Empty(t, events)
}

func TestReconcileWithLibrary_RealWorldSkillCraftPattern(t *testing.T) {
	// Sanity check using the exact proliferation pattern observed in
	// the v15 benchmark — kept here so a future regression on the
	// reconciler immediately shows up in CI rather than at benchmark
	// time. The names below are renamed slightly so the test does not
	// hard-code a specific benchmark domain.
	existing := []ExistingSkill{
		{Name: "Weather Monitor - Multi-City"},
		{Name: "Economic Snapshot - 3 Countries with APIs"},
	}
	// Each candidate has a unique (when_to_use, steps) shape so Rule 2
	// does not collapse them — we want Rule 1 to be the one doing all
	// four rewrites/drops in this scenario.
	in := &ReviewDecision{
		Skills: []*SkillSpec{
			{
				Name:        "Weather Monitor - Multi-City - 3 Cities and 3 APIs",
				Description: "d", WhenToUse: "wA", Steps: []string{"a1"},
			},
			{
				Name:        "Weather Monitor - Multi-City - 4 Cities with 4 APIs",
				Description: "d", WhenToUse: "wB", Steps: []string{"b1"},
			},
			{
				Name:        "Economic Snapshot - 3 Countries with APIs - E1",
				Description: "d", WhenToUse: "wC", Steps: []string{"c1"},
			},
			{
				Name:        "Economic Snapshot - 3 Countries with APIs - E3",
				Description: "d", WhenToUse: "wD", Steps: []string{"d1"},
			},
		},
	}
	out, _ := reconcileWithLibrary(in, existing)
	assert.Empty(t, out.Skills,
		"all four proliferation candidates should be rewritten or dropped")
	require.Len(t, out.Updates, 2,
		"each parent should receive exactly one update")
	targetNames := []string{out.Updates[0].Name, out.Updates[1].Name}
	assert.Contains(t, targetNames, "Weather Monitor - Multi-City")
	assert.Contains(t, targetNames, "Economic Snapshot - 3 Countries with APIs")
}

func TestReconcileWithLibrary_RealWorldWeatherSiblingPatternWithoutSuperset(t *testing.T) {
	existing := []ExistingSkill{
		{Name: "Weather Monitor - Multi-City"},
		{Name: "Weather Monitor - Multi-City with Historical Data"},
	}
	in := &ReviewDecision{
		Skills: []*SkillSpec{
			{
				Name:        "Weather Monitor - 3 Cities",
				Description: "d", WhenToUse: "wA", Steps: []string{"a1"},
			},
			{
				Name:        "Weather Monitor - 3 Cities with Historical Data",
				Description: "d", WhenToUse: "wB", Steps: []string{"b1"},
			},
			{
				Name:        "Weather Monitor - 4 Cities",
				Description: "d", WhenToUse: "wC", Steps: []string{"c1"},
			},
			{
				Name:        "Weather Monitor - 4 Cities with Historical Data",
				Description: "d", WhenToUse: "wD", Steps: []string{"d1"},
			},
			{
				Name:        "Weather Monitor - 5 Cities with Historical Data",
				Description: "d", WhenToUse: "wE", Steps: []string{"e1"},
			},
		},
	}

	out, _ := reconcileWithLibrary(in, existing)
	assert.Empty(t, out.Skills)
	require.Len(t, out.Updates, 2)
	targetNames := []string{out.Updates[0].Name, out.Updates[1].Name}
	assert.Contains(t, targetNames, "Weather Monitor - Multi-City")
	assert.Contains(t, targetNames, "Weather Monitor - Multi-City with Historical Data")
}
