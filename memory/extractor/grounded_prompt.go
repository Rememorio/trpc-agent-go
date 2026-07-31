//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import "strings"

var enhancedDefaultPrompt = strings.NewReplacer(
	legacyAtomicityGuideline,
	legacyAtomicityGuideline+selfContainedRelationsGuideline,
	legacyDeduplicationEnding,
	legacyDeduplicationEnding+groundedStateGuidelines,
	legacyChangedFactGuideline,
	groundedChangedFactGuideline,
	legacyDurationGuideline,
	groundedDurationGuideline,
	legacyDurationExample,
	groundedDurationExample,
).Replace(defaultPrompt)

const legacyAtomicityGuideline = `- **ATOMICITY**: Keep each memory focused on a SINGLE piece of information.
  For example, if a user says "I went to Paris with Alice and we ate at
  Le Cinq, then visited the Louvre", create SEPARATE memories for:
  the dinner at Le Cinq, the Louvre visit, and that Alice traveled with User.
`

const selfContainedRelationsGuideline = `- **SELF-CONTAINED RELATIONS**: An atomic memory must still be a complete
  proposition. Keep a relationship, its value, and every qualifier that scopes
  that value in the same memory. Do not split a count, choice, status, or other
  value from the named role, project, person, location, or time that makes it
  true. Good: "As Product Owner, leads three UX researchers." Bad: separate
  memories saying only "Is Product Owner" and "Leads three people" when the
  team size is specific to that role. Separate independent claims, not the
  arguments or qualifiers of one claim.
`

const legacyDeduplicationEnding = `  supporting signals and do NOT mean two different-day episodes are the same
  memory. When it is a duplicate, emit no tool call. When it corrects or
  replaces an existing memory, use memory_update.
`

const groundedStateGuidelines = `- **CURRENT-TURN GROUNDING**: Resolve pronouns, ellipsis, and terse follow-up
  answers from the nearest explicit question, label, or restatement in the
  current conversation. A later assistant restatement may clarify what the
  preceding user reply referred to. Existing memories are comparison context
  for deduplication, not evidence for choosing an ambiguous referent. Never
  attach one person's or object's new details to another existing memory merely
  because that memory is semantically similar.
- **SOURCE-FAITHFUL STATE**: Before writing a transition or lifecycle
  relationship, identify source words that explicitly state that relationship.
  If no such words exist, omit the relationship and write each supported claim
  as a separate atomic memory. Different sizes, names, or identifiers denote
  different subjects even when the objects share a category; acquiring or
  setting up one does not update another. Words such as "old", "new",
  "another", and "since" express age, identity, or sequence, not replacement
  or loss of ownership.
  Example: "I have an old laptop. I've since set up a new desktop." supports
  "Has an old laptop" and "Set up a new desktop". It does NOT support
  "The desktop replaced the laptop", "Moved on from the laptop", "Previously
  had the laptop", or "No longer owns the laptop". By contrast, explicit
  source wording such as "sold", "traded in", "replaced", "moved from", or
  "no longer owns" does support the corresponding state transition.
`

const legacyChangedFactGuideline = `- When a fact has genuinely CHANGED (e.g., user got a new job), update
  the existing memory. But if the conversation reveals a NEW fact, even
  on a related topic, create a NEW memory — do not merge into existing ones.
`

const groundedChangedFactGuideline = `- When a fact has explicitly and genuinely CHANGED (e.g., the user says they
  left one job and started another), update the existing memory. A related new
  fact does not prove that the old fact ended. Unless the conversation states
  a transition for the same subject, create a NEW memory and do not merge it
  into or use it to replace an existing one.
`

const legacyDurationGuideline = `- When someone mentions a duration (e.g., "painting for 7 years"), subtract
  the duration from today's date to derive the start date.
`

const groundedDurationGuideline = `- When someone mentions a duration (e.g., "painting for 7 years"), subtract
  the duration from today's date to derive the start date only when the main
  assertion is when the activity or relationship began.
- Anchor event_time to the main assertion, not automatically to the earliest
  date in the sentence. For a current cumulative state observed in this
  conversation (e.g., "has completed seven paintings since starting three
  months ago"), event_time is today's date because that is when the count is
  known to be seven. Preserve the derived start date in the memory text or as
  a separate start-date memory. Never move a current count backward to its
  "since" date.
`

const legacyDurationExample = `Example 4 – Duration-based date derivation:
  User says: "I've been painting for about 7 years now."
  (today = 2023-05-08)
  → memory_add(memory="User has been painting since approximately 2016.",
     memory_kind="fact", event_time="2016-01-01",
     topics=["painting", "hobby", "art"])
`

const groundedDurationExample = `Example 4 – Start-date derivation from a pure duration:
  User says: "I've been painting for about 7 years now."
  (today = 2023-05-08)
  → memory_add(memory="User has been painting since approximately 2016.",
     memory_kind="fact", event_time="2016-01-01",
     topics=["painting", "hobby", "art"])

Example 4b – Current cumulative state with a start boundary:
  User says: "I've been painting for three months and have completed seven
  paintings since I started."
  (today = 2023-05-30)
  → memory_add(memory="User has completed seven paintings since starting
     around late February 2023.", memory_kind="fact",
     event_time="2023-05-30", topics=["painting", "art", "progress"])
  The seven-painting count is observed today. 2023-02-28 describes when the
  activity began and must not be used as the count's event_time.
`
