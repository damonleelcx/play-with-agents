# 01 · Intents, tools and skills

This document lists everything the agent can be asked to do (**intents**), every action it can take in
the world (**tools**), and the multi-step playbooks it runs (**skills**). The planner, the worker, the
approval gates and the UI are all built from this document. A capability that is missing here does
not exist in the product.

---

## 0. The boundary everything else is built around

An AI system cannot hold a bar admission or a medical licence. It cannot appear in court as counsel,
sign a pleading, examine a patient, or write a prescription. Doing any of these without a licence is
unauthorised practice in every US state and in most other jurisdictions. That exposes **you** and
your clients, not only the software.

So the product is built as **one agent with two practices, supervised by licensed humans**:

| The agent does, on its own | A licensed professional does, with the agent's work in hand |
|---|---|
| Takes intake, researches, drafts, organises evidence, computes deadlines, prepares the hearing kit, and stays with the attorney during the hearing | **Appears in court**, signs, files, argues |
| Takes intake, triages, reasons through a differential, drafts the treatment plan, checks drugs, and follows up for weeks | **Diagnoses, orders tests, prescribes, treats** |

This split is enforced by **approval gates** (§4), not by prompt wording. A G2 tool call cannot run
until a user with the `attorney` or `physician` role, with a licence number on file, approves that
exact call.

---

## 1. Intents

The router classifies every user turn into exactly one intent. The intent then selects either a
single-step answer or a **skill**, which becomes a durable task DAG. `conf < 0.6` → ask one
clarifying question and do not guess.

### 1.1 Legal practice (`legal.*`)

| Intent | Example utterance | Routes to |
|---|---|---|
| `legal.intake` | "My landlord kept my deposit" | skill `new-matter` |
| `legal.explain` | "What is discovery?" | single answer |
| `legal.research` | "Is a verbal lease enforceable in CA?" | skill `legal-research-memo` |
| `legal.case_assessment` | "Do I have a case?" | skill `case-assessment` |
| `legal.draft_document` | "Draft a demand letter" | skill `demand-letter` / `motion-drafting` |
| `legal.review_document` | "Review this NDA" (upload) | skill `contract-review` |
| `legal.deadlines` | "When do I have to respond?" | skill `deadline-docketing` |
| `legal.court_prep` | "My hearing is on the 14th" | skill `hearing-preparation` |
| `legal.court_filing` | "File the answer" | skill `motion-drafting` → **G2** `court.efile` |
| `legal.hearing_support` | "We're in court now, they cited X" | live mode: `legal_research.*` + notes |
| `legal.negotiation` | "They offered $4,000" | skill `settlement-negotiation` |
| `legal.evidence` | "Here are my photos and texts" | skill `evidence-organisation` |
| `legal.client_update` | "Tell my client about the ruling" | **G1/G2** `email.send` |

### 1.2 Medical practice (`med.*`)

| Intent | Example utterance | Routes to |
|---|---|---|
| `med.emergency` | "Crushing chest pain", "I want to end my life" | **hard interrupt**: 911/988 guidance first, clinician paged, nothing else runs |
| `med.intake` | "I've had a headache for 3 days" | skill `symptom-assessment` |
| `med.triage` | "Should I go to the ER?" | skill `symptom-assessment` (triage node only) |
| `med.differential` | "What could this be?" | skill `symptom-assessment` |
| `med.results` | "Here are my lab results" | skill `lab-review` |
| `med.medications` | "Can I take ibuprofen with lisinopril?" | skill `medication-reconciliation` |
| `med.treatment_plan` | "What should I do about it?" | skill `symptom-assessment` → **G2** plan sign-off |
| `med.prescription` | "I need a refill" | **G2** `rx.submit`. **Never** for controlled substances (G3) |
| `med.test_order` | "Can I get a blood test?" | **G2** `lab.order` |
| `med.referral` | "I need a dermatologist" | **G2** `referral.send` |
| `med.follow_up` | "Check in on my BP every week" | skill `chronic-care-plan` (long-running) |
| `med.records` | "Summarise my hospital discharge" | skill `records-summary` |
| `med.explain` | "What is an A1C?" | single answer |
| `med.visit_prep` | "I'm seeing my doctor Friday" | skill `pre-visit-summary` |

### 1.3 Cross-practice (`medlegal.*`). This is what one agent with both practices is for.

| Intent | Example | Routes to |
|---|---|---|
| `medlegal.injury_claim` | "I was hurt in a car accident" | skill `personal-injury-case` |
| `medlegal.insurance_denial` | "Insurance denied my MRI" | skill `insurance-appeal` |
| `medlegal.workers_comp` | "I got hurt at work" | skill `workers-comp-claim` |
| `medlegal.malpractice` | "I think my surgery went wrong" | skill `malpractice-screen` |
| `medlegal.records_request` | "Get my records from the hospital" | skill `records-request` (HIPAA authorisation) |
| `medlegal.disability` | "Apply for disability" | skill `disability-claim` |

### 1.4 Workflow control (`goal.*`, `approval.*`, `account.*`)

| Intent | Example | Effect |
|---|---|---|
| `goal.status` | "Where are we?", "What happens next?" | Timeline read: what happened, why, when, and what comes next |
| `goal.pause` / `goal.resume` / `goal.cancel` | "Hold off on this" | State transition with safe cancellation (§5) |
| `goal.change` | "Actually the hearing moved to the 20th" | Triggers **replan** |
| `approval.respond` | "Yes, send it" | Resolves the pending gate. The user can approve only gates their role allows |
| `schedule.reminder` | "Remind me Monday" | Scheduler wake-up |
| `document.upload` | (file) | Parse + index + attach to the matter/case |
| `account.preferences` | "Talk to me in Spanish", "Be briefer" | Preference memory write |
| `smalltalk` | "Who are you?" | Persona answer (soul doc) |
| `refuse.*` | Evidence tampering, perjury coaching, drug-seeking for controlled substances, fraud | Decline with the reason, and offer the lawful path |

---

## 2. Tools

Every tool has a **typed input schema, a typed output schema, a timeout, a retry policy, an
idempotency rule, a gate class, and a verifier**. The worker does not trust a tool's return value.
The verifier checks that the effect actually happened (§2.4).

Side-effect classes: **R** = read-only, **W** = internal write (our DB), **X** = external side effect.

### 2.1 Legal tools

| Tool | Class | Gate | Backing | Verifier |
|---|---|---|---|---|
| `legal_research.search_cases` | R | G0 | CourtListener REST API (free, real) | results non-empty or explicit "none found" |
| `legal_research.get_opinion` | R | G0 | CourtListener | opinion id resolves |
| `legal_research.verify_citation` | R | G0 | CourtListener citation lookup | **every** citation in every draft must resolve, or the draft fails verification (anti-hallucination) |
| `legal_research.statute` | R | G0 | govinfo / eCFR / state code sites | section text retrieved |
| `legal.deadline_calc` | R | G0 | Deterministic court-rule calculator (FRCP 6 + state tables) | recompute and compare |
| `legal.conflict_check` | R | G0 | Our parties table | — |
| `legal.draft` | W | G0 | LLM + templates → `documents` row (versioned) | document persisted, all citations verified |
| `legal.evidence_index` | W | G0 | Exhibit list, Bates numbering, hash per file | SHA-256 recorded per exhibit |
| `court.efile` | X | **G2 attorney** | Adapter interface. Sandbox implementation ships; a live one needs an EFSP account (e.g. Tyler Odyssey) | envelope id + court acceptance status polled |

### 2.2 Medical tools

| Tool | Class | Gate | Backing | Verifier |
|---|---|---|---|---|
| `med.triage_rules` | R | G0 | Deterministic red-flag rules. **They run before the LLM** and cannot be overridden by it | — |
| `med.icd10_lookup` | R | G0 | NLM Clinical Tables API (free) | code exists |
| `med.drug_label` | R | G0 | openFDA drug label API (free) | label found |
| `med.drug_normalise` | R | G0 | NLM RxNorm (free) | rxcui resolves |
| `med.interaction_check` | R | G0 | openFDA label interaction sections + curated high-risk pair table | — |
| `med.dose_calc` | R | G0 | Deterministic (weight, CrCl / Cockcroft-Gault) | recompute |
| `med.literature` | R | G0 | PubMed E-utilities (free) | PMIDs resolve |
| `med.plan_draft` | W | G0 | LLM → `care_plans` row (versioned) | persisted, and every drug passes the interaction check |
| `lab.order` | X | **G2 physician** | Adapter (sandbox; live needs a lab network contract) | order id + status |
| `rx.submit` | X | **G2 physician**; **G3** for schedules II–V | Adapter (sandbox; live needs Surescripts/DoseSpot) | pharmacy acknowledgement |
| `referral.send` | X | **G2 physician** | Adapter / email | delivery receipt |

### 2.3 Shared tools

| Tool | Class | Gate | Notes |
|---|---|---|---|
| `kb.retrieve` | R | G0 | Postgres full-text + pgvector over uploaded docs and firm knowledge |
| `document.parse` | W | G0 | PDF/DOCX/image → text, chunked and indexed |
| `memory.read` / `memory.write` | R/W | G0 | Preferences and reusable knowledge (§6) |
| `email.send` | X | **G1 client** for the client's own mail; **G2** for mail to third parties / opposing counsel | Idempotency key = `(task_id, step)`. Sent through the mail relay; verified by the relay's 250 + message-id |
| `calendar.create_event` | X | G1 | Idempotent on `(matter_id, deadline_id)`; ICS attached to mail |
| `escalate.emergency` | X | **none, fires immediately** | Shows 911/988, pages the on-call clinician |
| `handoff.professional` | W | G0 | Puts the matter/case in a licensed reviewer's queue |
| `notify.user` | X | G0 | In-app + optional email digest |
| `schedule.wake` | W | G0 | Creates a timer the scheduler honours |

### 2.4 The tool contract (enforced in code)

```
Tool {
  name, version
  input:  JSON Schema   → validated before execution, rejected otherwise
  output: JSON Schema   → validated after execution; failure = tool error
  sideEffect: R | W | X
  gate: G0 | G1 | G2(role) | G3
  timeout, retry{max, backoff, retryOn[]}
  idempotencyKey(input, task) → string   (X tools only; stored in tool_calls with a unique index)
  verify(input, output) → ok | mismatch(reason)
}
```

---

## 3. Skills (playbooks → task DAGs)

A skill is a **template DAG**. The planner instantiates it into durable `tasks` rows with
dependencies, then keeps adapting it (replan). Every skill declares its completion criteria, so
"done" is measurable.

### 3.1 Legal skills

| Skill | DAG (→ = depends on) | Gates | Done when |
|---|---|---|---|
| `new-matter` | intake → conflict_check → jurisdiction → deadlines → case_assessment → engagement_summary | none | matter has parties, jurisdiction and docketed deadlines |
| `legal-research-memo` | frame_question → search_cases ∥ statutes → synthesise → **verify_citations** → memo | none | memo persisted, 100% of citations resolve |
| `case-assessment` | facts → elements checklist → research → strengths/risks → options | none | assessment persisted |
| `demand-letter` | facts → research → draft → verify_citations → client_review → send | G1 (client), G2 if signed as counsel | relay accepted the message |
| `contract-review` | parse → clause extraction → risk scoring → redlines → summary | none | redline document persisted |
| `motion-drafting` | research → outline → draft → verify_citations → **attorney_review** → efile → poll acceptance | **G2** attorney | court accepted the filing |
| `hearing-preparation` | deadlines → exhibits → argument outline → anticipated questions → mock cross → hearing kit | G2 review of the kit | kit approved ≥ 24h before the hearing (scheduler wakes at T-7d, T-2d, T-1d) |
| `evidence-organisation` | parse uploads → hash → bates → exhibit list | none | every file hashed and indexed |
| `settlement-negotiation` | valuation → strategy → draft response → client approval → send | G1 + G2 | response sent and logged |
| `deadline-docketing` | extract triggers → calculate → calendar events → reminders | G1 calendar | every deadline has a calendar event and a reminder |

### 3.2 Medical skills

| Skill | DAG | Gates | Done when |
|---|---|---|---|
| `symptom-assessment` | **red_flag_rules** → intake → history/meds/allergies → triage → differential → suggested tests → draft plan → **physician_review** | **G2** physician for plan / tests / Rx | plan signed or the patient is referred |
| `lab-review` | parse report → normalise units → flag out-of-range → interpretation → physician_review | G2 | interpretation signed |
| `medication-reconciliation` | list meds → normalise (RxNorm) → interaction check → dose check → report | G2 for any change | report persisted |
| `chronic-care-plan` | baseline → plan → **weekly check-in (scheduled wake)** → trend → escalate on threshold → monthly physician review | G2 monthly | runs for weeks. Stops on the physician's close-out or the patient's cancel |
| `pre-visit-summary` | gather records → timeline → questions for the doctor | none | summary persisted |
| `records-summary` | parse → timeline → problem list → meds → summary | none | summary persisted |

### 3.3 Cross-practice skills

| Skill | DAG | Gates |
|---|---|---|
| `personal-injury-case` | medical records-request → records-summary → causation analysis → damages (medical specials + future care) → liability research → demand package → negotiation | G1, G2 attorney |
| `insurance-appeal` | parse denial → medical-necessity letter (physician) ∥ policy/regulatory research (legal) → appeal packet → send → track deadline | G2 physician + G2 attorney |
| `workers-comp-claim` | injury intake → treatment timeline → state-form selection → filing deadline → claim packet | G2 attorney |
| `malpractice-screen` | records → standard-of-care research → expert-need flag → SOL calculation → attorney handoff | G2 |
| `records-request` | HIPAA authorisation draft → client e-sign → send to provider → wait (30-day timer) → follow up → ingest | G1 |
| `disability-claim` | medical evidence → functional limitations → form mapping → packet | G2 |

### 3.4 Harness skills (the agent's own machinery, not user-facing)

| Skill | Role |
|---|---|
| `route` | Classifies the intent. Emergency rules run first and deterministically |
| `plan` / `replan` | Instantiates a skill DAG, then compares state to the goal on a schedule and after every failure or `goal.change` |
| `verify` | Per-step verifier plus an LLM judge for drafts (citations, red flags, completeness) |
| `compact` | Summarises old history into episodic summaries so the prompt never grows unbounded |
| `persona` | Applies the soul doc's voice to user-facing text only, never to tool arguments |
| `policy` | Checks the refusal list and gates before every tool call |

---

## 4. Approval gates

| Gate | Who approves | Examples |
|---|---|---|
| **G0** | nobody | research, drafting, internal writes |
| **G1** | the client (account owner) | sending their own letter, calendar events, sharing records |
| **G2** | a user with role `attorney` / `physician` **and a licence on file** | e-filing, mail to opposing counsel, lab orders, prescriptions, referrals, care-plan sign-off |
| **G3** | **never automated** | appearing as counsel, signing as attorney/physician, controlled-substance prescriptions, destroying evidence |

A gate stores the exact proposed call (tool, arguments, idempotency key, preview). Approving one call
never approves a later one.

## 5. Limits (defaults, per goal)

Max iterations 200, tool calls 500, tokens 2M, cost $20, wall-clock 30 days, DAG depth 6, tasks
created per replan 20, retries per task 5 (exponential backoff 2s→10min, with jitter). When any of
these is hit, the goal moves to `needs_attention`. It never fails silently.

## 6. Memory types

| Type | Table | Lifetime |
|---|---|---|
| Task state | `goals`, `tasks`, `task_deps`, `checkpoints` | per goal |
| Episodic | `events` (append-only) + `episode_summaries` | forever; compacted |
| Knowledge | `knowledge_chunks` (docs, research memos) | per matter/case, reusable |
| Preferences | `user_preferences` | per user |
