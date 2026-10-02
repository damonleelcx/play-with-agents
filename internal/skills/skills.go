// Package skills holds the playbooks. Each is a template DAG the planner
// instantiates into durable tasks and may tailor — within the tools the skill
// allows. See docs/01-intents-tools-skills.md §3.
package skills

import "sort"

type Step struct {
	Key          string
	Title        string
	Instructions string
	Tools        []string
	Deps         []string
	// Verify names deterministic checks the worker runs on the step's result
	// before accepting it: "document_saved", "citations_verified", "signed_off".
	Verify []string
	// WaitDays turns the step into a timer: it succeeds WaitDays after its
	// dependencies finish. Used for check-ins and follow-ups.
	WaitDays int
	MaxSteps int
}

type Skill struct {
	Name        string
	Domain      string // legal | medical | medlegal | general
	Title       string
	TitleZH     string
	Description string
	Criteria    []string
	Milestones  []string
	Steps       []Step
	// Recurring skills re-plan themselves after the last step instead of
	// finishing, until the client or a professional closes them.
	Recurring bool
}

var all = map[string]*Skill{}

func add(s *Skill) { all[s.Name] = s }

func Get(name string) (*Skill, bool) { s, ok := all[name]; return s, ok }

func All() []*Skill {
	out := make([]*Skill, 0, len(all))
	for _, s := range all {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Tools shared by nearly every step.
var (
	base     = []string{"kb_search", "get_document", "memory_save", "notify_user"}
	research = []string{"legal_search_cases", "legal_statute_lookup", "legal_verify_citations"}
	drafting = []string{"save_document", "legal_verify_citations"}
	clinical = []string{"med_red_flag_check", "med_icd10_lookup", "med_drug_normalize", "med_drug_label", "med_interaction_check", "med_dose_calc", "med_pubmed_search"}
	signoffA = []string{"request_professional_signoff"}
	remind   = []string{"schedule_reminder"}
)

func tl(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range append([][]string{base}, groups...) {
		for _, t := range g {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

func init() {
	// ── Legal ────────────────────────────────────────────────────────────────
	add(&Skill{Name: "new-matter", Domain: "legal", Title: "Open a new legal matter", TitleZH: "新建法律事务",
		Description: "Intake a new legal problem: facts, parties, jurisdiction, conflicts, deadlines, and an initial assessment.",
		Criteria:    []string{"Parties, jurisdiction and key facts are recorded", "Every known deadline is computed and scheduled", "An initial assessment is saved"},
		Milestones:  []string{"Intake", "Deadlines docketed", "Assessment ready"},
		Steps: []Step{
			{Key: "intake", Title: "Organise the facts and parties", Tools: tl(), Instructions: "From the conversation and any uploaded documents, write a structured intake: parties (with roles), timeline of events, jurisdiction (state/county/court if known), what the client wants, and what is still unknown. Save it as a memo titled 'Intake'. Save durable facts (e.g. the client's state) with memory_save.", Verify: []string{"document_saved"}},
			{Key: "conflicts", Title: "Check for conflicts", Deps: []string{"intake"}, Tools: tl([]string{"legal_conflict_check"}), Instructions: "Run a conflict check on every opposing party named in the intake. Report any hits."},
			{Key: "deadlines", Title: "Compute and docket deadlines", Deps: []string{"intake"}, Tools: tl([]string{"legal_deadline_calc"}, remind), Instructions: "Identify every deadline implied by the facts (statutes of limitation, response deadlines, hearing dates). Compute each with legal_deadline_calc, never by hand, and schedule a reminder 7 days and 1 day before each. If a trigger date is unknown, say so explicitly rather than guessing."},
			{Key: "assessment", Title: "Initial case assessment", Deps: []string{"conflicts", "deadlines"}, Tools: tl(research, drafting), Instructions: "Write a plain-language initial assessment: the legal elements the client must prove, how the facts map to each, strengths, risks, options (including doing nothing), and next steps. Cite real authority only; verify every citation. Save it as a memo.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "report", Title: "Tell the client where things stand", Deps: []string{"assessment"}, Tools: tl(), Instructions: "Send the client a short update with notify_user: what you found, the deadlines, and what happens next. Remind them this is not yet reviewed by an attorney."},
		}})

	add(&Skill{Name: "legal-research-memo", Domain: "legal", Title: "Legal research memo", TitleZH: "法律研究备忘录",
		Description: "Answer a legal question with a verified research memo.",
		Criteria:    []string{"The memo answers the question", "100% of cited cases resolve on CourtListener"},
		Milestones:  []string{"Question framed", "Authorities found", "Memo verified"},
		Steps: []Step{
			{Key: "frame", Title: "Frame the question", Tools: tl(), Instructions: "State the precise legal question, the jurisdiction, and the facts that matter. If jurisdiction is unknown, state the assumption you are making."},
			{Key: "search", Title: "Find authority", Deps: []string{"frame"}, Tools: tl(research), Instructions: "Search case law and statutes. Prefer binding authority in the jurisdiction. Record each authority with its citation, holding, and link.", MaxSteps: 10},
			{Key: "memo", Title: "Write and verify the memo", Deps: []string{"search"}, Tools: tl(research, drafting), Instructions: "Write a memo: Question, Short Answer, Facts, Analysis, Conclusion. Run legal_verify_citations on the full text; remove or replace any unverified citation. Save the memo.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "report", Title: "Share the answer", Deps: []string{"memo"}, Tools: tl(), Instructions: "notify_user with the short answer in plain language and that the memo is in their file."},
		}})

	add(&Skill{Name: "case-assessment", Domain: "legal", Title: "Case assessment", TitleZH: "案件评估",
		Description: "Do I have a case? Elements, strengths, risks and options.",
		Criteria:    []string{"Assessment saved with verified citations"},
		Milestones:  []string{"Elements mapped", "Assessment ready"},
		Steps: []Step{
			{Key: "elements", Title: "Map facts to legal elements", Tools: tl(research), Instructions: "Identify the claims or defences available and the elements of each. Map the known facts to each element and list the evidence needed for gaps."},
			{Key: "assess", Title: "Assess strengths, risks and options", Deps: []string{"elements"}, Tools: tl(research, drafting), Instructions: "Write the assessment: strengths, weaknesses, likely range of outcomes, costs and time, and options including settlement. Verify citations. Save it.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "report", Title: "Explain it to the client", Deps: []string{"assess"}, Tools: tl(), Instructions: "notify_user with a plain-language summary."},
		}})

	add(&Skill{Name: "demand-letter", Domain: "legal", Title: "Demand letter", TitleZH: "律师函 / 催告函",
		Description: "Draft, verify and (with approval) send a demand letter.",
		Criteria:    []string{"Letter saved and approved", "Letter accepted by the mail relay, or the client chose to send it themselves"},
		Milestones:  []string{"Draft", "Client approval", "Sent"},
		Steps: []Step{
			{Key: "facts", Title: "Confirm the facts and the demand", Tools: tl(), Instructions: "Establish the recipient (name, email or address), the amount or action demanded, the deadline to respond, and the legal basis. If the recipient's email is unknown, the letter will be delivered to the client to send."},
			{Key: "research", Title: "Find the legal basis", Deps: []string{"facts"}, Tools: tl(research), Instructions: "Find the statute or case law that supports the demand in the client's jurisdiction (e.g. security-deposit statutes)."},
			{Key: "draft", Title: "Draft the letter", Deps: []string{"research"}, Tools: tl(drafting, []string{"legal_deadline_calc"}), Instructions: "Draft a firm, professional demand letter in the client's name (not as an attorney unless an attorney will sign). Compute the response deadline with legal_deadline_calc. Verify citations. Save it as a letter.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "send", Title: "Send with the client's approval", Deps: []string{"draft"}, Tools: tl([]string{"email_send"}, remind), Instructions: "If a recipient email is known, send the letter with email_send on_behalf_of='client' (the client approves it first). Otherwise email it to the client's own address so they can send it. Schedule a reminder on the response deadline."},
		}})

	add(&Skill{Name: "contract-review", Domain: "legal", Title: "Contract review", TitleZH: "合同审查",
		Description: "Review an uploaded contract: clauses, risks, redlines.",
		Criteria:    []string{"Review saved with risk-rated clauses and proposed redlines"},
		Milestones:  []string{"Clauses extracted", "Review ready"},
		Steps: []Step{
			{Key: "extract", Title: "Extract the key clauses", Tools: tl(), Instructions: "Find the contract in the client's file (kb_search / get_document). Extract parties, term, payment, termination, liability caps, indemnities, IP, confidentiality, non-compete, governing law, dispute resolution, auto-renewal."},
			{Key: "review", Title: "Rate risks and propose redlines", Deps: []string{"extract"}, Tools: tl(research, drafting), Instructions: "For each clause: risk (low/medium/high) from the client's side, why, and a proposed redline in exact replacement language. Add a one-paragraph summary at the top. Save as contract_review.", Verify: []string{"document_saved"}},
			{Key: "report", Title: "Summarise for the client", Deps: []string{"review"}, Tools: tl(), Instructions: "notify_user with the three most important issues."},
		}})

	add(&Skill{Name: "motion-drafting", Domain: "legal", Title: "Draft and file a court document", TitleZH: "起草并提交诉讼文书",
		Description: "Research, draft, verify, attorney review, then e-file.",
		Criteria:    []string{"Document drafted with verified citations", "Signed off by a licensed attorney", "Filing accepted"},
		Milestones:  []string{"Research", "Draft", "Attorney sign-off", "Filed"},
		Steps: []Step{
			{Key: "research", Title: "Research", Tools: tl(research), Instructions: "Research the governing rules and authority for this filing in the court at issue.", MaxSteps: 10},
			{Key: "draft", Title: "Draft the filing", Deps: []string{"research"}, Tools: tl(research, drafting, []string{"legal_deadline_calc"}), Instructions: "Draft the document in the court's expected structure (caption, introduction, facts, argument, conclusion). Verify every citation. Save it as a pleading.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "review", Title: "Attorney review", Deps: []string{"draft"}, Tools: tl(signoffA), Instructions: "Request attorney sign-off on the saved draft with a summary of what it argues and any weak points.", Verify: []string{"signed_off"}},
			{Key: "file", Title: "E-file", Deps: []string{"review"}, Tools: tl([]string{"court_efile"}), Instructions: "E-file the approved document with court_efile. The attorney approves the exact filing."},
			{Key: "report", Title: "Confirm to the client", Deps: []string{"file"}, Tools: tl(remind), Instructions: "notify_user with the envelope id and status. Schedule reminders for any response deadline that the filing triggers."},
		}})

	add(&Skill{Name: "hearing-preparation", Domain: "legal", Title: "Hearing preparation", TitleZH: "庭审准备",
		Description: "Build the hearing kit for the attorney who will appear: argument outline, exhibits, questions, mock cross.",
		Criteria:    []string{"Hearing kit saved", "Kit signed off by the appearing attorney", "Reminders set before the hearing"},
		Milestones:  []string{"Exhibits", "Arguments", "Kit approved"},
		Steps: []Step{
			{Key: "schedule", Title: "Docket the hearing", Tools: tl([]string{"legal_deadline_calc"}, remind), Instructions: "Confirm the hearing date, court and judge if known. Schedule reminders 7, 2 and 1 day before. Compute any pre-hearing filing deadlines."},
			{Key: "exhibits", Title: "Organise exhibits", Deps: []string{"schedule"}, Tools: tl(drafting), Instructions: "Build an exhibit list from the client's documents: number, description, what it proves, source. Save as evidence_index.", Verify: []string{"document_saved"}},
			{Key: "arguments", Title: "Argument outline", Deps: []string{"schedule"}, Tools: tl(research, drafting), Instructions: "Outline the arguments in order of strength with supporting authority, and the opposing side's best arguments with responses."},
			{Key: "kit", Title: "Assemble the hearing kit", Deps: []string{"exhibits", "arguments"}, Tools: tl(research, drafting), Instructions: "Assemble the kit: one-page summary, argument outline, anticipated questions from the bench with answers, cross-examination outline and a mock cross of the client, exhibit list, and what to bring. Verify citations. Save as hearing_kit.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "review", Title: "Appearing attorney signs off", Deps: []string{"kit"}, Tools: tl(signoffA), Instructions: "Request attorney sign-off on the hearing kit.", Verify: []string{"signed_off"}},
			{Key: "brief", Title: "Brief the client", Deps: []string{"review"}, Tools: tl(), Instructions: "notify_user: what to wear, when to arrive, what to bring, and what will happen, in plain language."},
		}})

	add(&Skill{Name: "evidence-organisation", Domain: "legal", Title: "Organise evidence", TitleZH: "证据整理",
		Description: "Index uploaded evidence into an exhibit list.",
		Criteria:    []string{"Exhibit index saved"},
		Steps: []Step{
			{Key: "index", Title: "Build the exhibit index", Tools: tl(drafting), Instructions: "Index every evidence document in the client's file: exhibit number, date, description, relevance, and gaps in the evidence. Save as evidence_index.", Verify: []string{"document_saved"}},
			{Key: "report", Title: "Report gaps", Deps: []string{"index"}, Tools: tl(), Instructions: "notify_user with the evidence still missing."},
		}})

	add(&Skill{Name: "settlement-negotiation", Domain: "legal", Title: "Settlement negotiation", TitleZH: "和解谈判",
		Description: "Value the claim, set a strategy and draft the response.",
		Criteria:    []string{"Valuation and response saved", "Response sent or handed to the client"},
		Steps: []Step{
			{Key: "value", Title: "Value the claim", Tools: tl(research, drafting), Instructions: "Estimate a realistic settlement range with reasoning (damages, fees, risk discount, time). Save as memo.", Verify: []string{"document_saved"}},
			{Key: "draft", Title: "Draft the response", Deps: []string{"value"}, Tools: tl(drafting), Instructions: "Draft the counter-offer or acceptance letter. Save it.", Verify: []string{"document_saved"}},
			{Key: "send", Title: "Send with approval", Deps: []string{"draft"}, Tools: tl([]string{"email_send"}), Instructions: "Send the response. Use on_behalf_of='counsel' if it goes to opposing counsel; otherwise 'client'."},
		}})

	add(&Skill{Name: "deadline-docketing", Domain: "legal", Title: "Deadline docketing", TitleZH: "期限管理",
		Description: "Compute and schedule every deadline.",
		Criteria:    []string{"Every deadline computed deterministically and scheduled"},
		Steps: []Step{
			{Key: "docket", Title: "Compute and schedule deadlines", Tools: tl([]string{"legal_deadline_calc"}, remind, drafting), Instructions: "List every trigger event and the rule behind it. Compute each deadline with legal_deadline_calc. Schedule reminders 7 and 1 day before. Save a deadline table as a memo.", Verify: []string{"document_saved"}},
		}})

	// ── Medical ──────────────────────────────────────────────────────────────
	add(&Skill{Name: "symptom-assessment", Domain: "medical", Title: "Symptom assessment", TitleZH: "症状评估",
		Description: "Red flags, intake, triage, differential, suggested work-up, draft plan, physician review.",
		Criteria:    []string{"Red flags screened", "Clinical summary with differential saved", "Plan signed off by a physician, or the client referred to in-person care"},
		Milestones:  []string{"Triage", "Differential", "Physician review"},
		Steps: []Step{
			{Key: "triage", Title: "Screen for emergencies and triage", Tools: tl([]string{"med_red_flag_check", "escalate_emergency"}), Instructions: "Run med_red_flag_check on everything the client described. If emergency=true, call escalate_emergency and tell the client to call emergency services; the plan stops there. Otherwise assign a triage level (emergency / urgent within 24h / soon within days / routine / self-care) with reasons."},
			{Key: "history", Title: "Structured history", Deps: []string{"triage"}, Tools: tl(), Instructions: "Organise the history: chief complaint, onset, location, duration, character, aggravating/relieving factors, associated symptoms, timing, severity; past history, medications, allergies, social and family history. List what is missing that a clinician would ask. Save durable facts (allergies, chronic conditions, medications) with memory_save."},
			{Key: "differential", Title: "Differential diagnosis", Deps: []string{"history"}, Tools: tl(clinical, drafting), Instructions: "Write a ranked differential (most likely, must-not-miss, others) with reasoning for and against each, ICD-10 codes, and the questions or tests that would distinguish them. Cite evidence (PubMed) where useful. Save as clinical_summary.", Verify: []string{"document_saved"}, MaxSteps: 10},
			{Key: "plan", Title: "Draft a plan for physician review", Deps: []string{"differential"}, Tools: tl(clinical, drafting), Instructions: "Draft a management plan: suggested tests, self-care, any medication with dose (check interactions against the client's list and calculate doses deterministically), red flags to watch, and follow-up timing. Mark every item that requires a physician order. Save as care_plan.", Verify: []string{"document_saved"}},
			{Key: "review", Title: "Physician review", Deps: []string{"plan"}, Tools: tl(signoffA), Instructions: "Request physician sign-off on the care plan with a concise clinical summary.", Verify: []string{"signed_off"}},
			{Key: "orders", Title: "Carry out approved orders", Deps: []string{"review"}, Tools: tl([]string{"lab_order", "rx_submit", "referral_send"}, remind), Instructions: "For each item the physician approved that needs an order, submit it (each is approved individually). Schedule a follow-up check-in reminder."},
			{Key: "explain", Title: "Explain the plan to the client", Deps: []string{"orders"}, Tools: tl(drafting), Instructions: "Write patient education in plain language: what it likely is, what to do, what to avoid, and exactly when to seek urgent care. Save as patient_education and notify_user."},
		}})

	add(&Skill{Name: "lab-review", Domain: "medical", Title: "Lab results review", TitleZH: "检验结果解读",
		Description: "Interpret lab results with physician review.",
		Criteria:    []string{"Interpretation saved and signed off"},
		Steps: []Step{
			{Key: "parse", Title: "Read and normalise the results", Tools: tl(), Instructions: "Find the lab report in the client's file. List each test, value, unit and reference range; flag out-of-range values."},
			{Key: "interpret", Title: "Interpret", Deps: []string{"parse"}, Tools: tl(clinical, drafting), Instructions: "Interpret the pattern in context of the client's history. Separate what is clearly normal, what is mildly abnormal, and what needs attention. Save as clinical_summary.", Verify: []string{"document_saved"}},
			{Key: "review", Title: "Physician review", Deps: []string{"interpret"}, Tools: tl(signoffA), Instructions: "Request physician sign-off.", Verify: []string{"signed_off"}},
			{Key: "explain", Title: "Explain to the client", Deps: []string{"review"}, Tools: tl(), Instructions: "notify_user in plain language."},
		}})

	add(&Skill{Name: "medication-reconciliation", Domain: "medical", Title: "Medication check", TitleZH: "用药核查",
		Description: "Normalise medications, check interactions and doses.",
		Criteria:    []string{"Every medication normalised and checked", "Report saved"},
		Steps: []Step{
			{Key: "list", Title: "Normalise the medication list", Tools: tl(clinical), Instructions: "List every medication, supplement and dose the client takes. Normalise each with med_drug_normalize. Save the list to memory."},
			{Key: "check", Title: "Check interactions and doses", Deps: []string{"list"}, Tools: tl(clinical, drafting), Instructions: "Run med_interaction_check on the full list. Check doses against labels, adjusting for kidney function if known (med_dose_calc crcl). Save a report as clinical_summary with each issue's severity and suggested action.", Verify: []string{"document_saved"}},
			{Key: "review", Title: "Physician review of any change", Deps: []string{"check"}, Tools: tl(signoffA), Instructions: "If the report recommends any change, request physician sign-off. If nothing needs changing, say so and skip sign-off by finishing."},
			{Key: "report", Title: "Tell the client", Deps: []string{"review"}, Tools: tl(), Instructions: "notify_user: what is safe, what to ask their doctor about, and never to stop a medication without talking to their prescriber."},
		}})

	add(&Skill{Name: "chronic-care-plan", Domain: "medical", Title: "Ongoing care plan", TitleZH: "长期健康管理", Recurring: true,
		Description: "Baseline, plan and weekly check-ins that run for weeks, with monthly physician review.",
		Criteria:    []string{"Weekly check-ins happen on schedule", "Out-of-range readings escalate", "Physician reviews monthly"},
		Milestones:  []string{"Baseline", "Week 1", "Week 2", "Week 3", "Week 4 review"},
		Steps: []Step{
			{Key: "baseline", Title: "Record the baseline", Tools: tl(clinical, drafting), Instructions: "Record the condition, current readings, targets (e.g. BP < 130/80), medications and lifestyle factors. Save a care_plan with targets and escalation thresholds.", Verify: []string{"document_saved"}},
			{Key: "wait1", Title: "Wait one week", Deps: []string{"baseline"}, WaitDays: 7},
			{Key: "checkin1", Title: "Weekly check-in", Deps: []string{"wait1"}, Tools: tl(clinical, []string{"escalate_emergency"}), Instructions: "Ask the client (notify_user) for this week's readings and symptoms. If readings they already sent exceed escalation thresholds, escalate. Summarise the trend."},
			{Key: "wait2", Title: "Wait one week", Deps: []string{"checkin1"}, WaitDays: 7},
			{Key: "checkin2", Title: "Weekly check-in", Deps: []string{"wait2"}, Tools: tl(clinical, []string{"escalate_emergency"}), Instructions: "Same as the previous check-in, comparing against the trend so far."},
			{Key: "review", Title: "Physician review of the month", Deps: []string{"checkin2"}, Tools: tl(signoffA, drafting), Instructions: "Save a trend summary and request physician sign-off on continuing or adjusting the plan.", Verify: []string{"document_saved", "signed_off"}},
		}})

	add(&Skill{Name: "pre-visit-summary", Domain: "medical", Title: "Prepare for a doctor's visit", TitleZH: "就诊准备",
		Description: "A one-page summary and questions to bring to an appointment.",
		Criteria:    []string{"Summary saved"},
		Steps: []Step{
			{Key: "summary", Title: "Write the visit summary", Tools: tl(clinical, drafting), Instructions: "One page: reason for visit, symptom timeline, medications and allergies, relevant history, and the 5 most useful questions to ask. Save as clinical_summary.", Verify: []string{"document_saved"}},
			{Key: "report", Title: "Share it", Deps: []string{"summary"}, Tools: tl(), Instructions: "notify_user that it is ready to print or show."},
		}})

	add(&Skill{Name: "records-summary", Domain: "medical", Title: "Summarise medical records", TitleZH: "病历摘要",
		Description: "Timeline, problem list and medications from records.",
		Criteria:    []string{"Summary saved"},
		Steps: []Step{
			{Key: "summary", Title: "Summarise the records", Tools: tl(clinical, drafting), Instructions: "From the client's uploaded records: timeline of care, problem list, medications, allergies, procedures, pending items. Save as records_summary.", Verify: []string{"document_saved"}},
			{Key: "report", Title: "Share it", Deps: []string{"summary"}, Tools: tl(), Instructions: "notify_user with the highlights."},
		}})

	// ── Both ─────────────────────────────────────────────────────────────────
	add(&Skill{Name: "personal-injury-case", Domain: "medlegal", Title: "Personal injury claim", TitleZH: "人身伤害索赔",
		Description: "Medical records → causation → damages → liability → demand package.",
		Criteria:    []string{"Medical summary and causation analysis saved", "Damages computed", "Demand package signed off by an attorney"},
		Milestones:  []string{"Medical picture", "Liability", "Damages", "Demand"},
		Steps: []Step{
			{Key: "deadlines", Title: "Statute of limitations", Tools: tl([]string{"legal_deadline_calc"}, research, remind), Instructions: "Determine the limitation period for the injury in the client's state and compute the deadline. Schedule reminders 90, 30 and 7 days before."},
			{Key: "medical", Title: "Medical picture", Tools: tl(clinical, drafting), Instructions: "Summarise injuries, treatment, prognosis and future care needs from records and the client's account. Save as records_summary.", Verify: []string{"document_saved"}},
			{Key: "liability", Title: "Liability research", Tools: tl(research, drafting), Instructions: "Research negligence elements and comparative fault rules in the jurisdiction. Save a memo with verified citations.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "damages", Title: "Damages", Deps: []string{"medical"}, Tools: tl(drafting), Instructions: "Compute economic damages (medical bills, lost wages, future care) and describe non-economic damages. Save as memo.", Verify: []string{"document_saved"}},
			{Key: "demand", Title: "Demand package", Deps: []string{"deadlines", "liability", "damages"}, Tools: tl(research, drafting), Instructions: "Assemble the demand package to the insurer. Verify citations. Save as letter.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "review", Title: "Attorney review", Deps: []string{"demand"}, Tools: tl(signoffA), Instructions: "Request attorney sign-off on the demand package.", Verify: []string{"signed_off"}},
			{Key: "send", Title: "Send the demand", Deps: []string{"review"}, Tools: tl([]string{"email_send"}), Instructions: "If the adjuster's email is known, send with on_behalf_of='counsel'. Otherwise deliver to the client."},
		}})

	add(&Skill{Name: "insurance-appeal", Domain: "medlegal", Title: "Insurance denial appeal", TitleZH: "保险拒赔申诉",
		Description: "Medical-necessity letter plus policy and regulatory argument.",
		Criteria:    []string{"Appeal packet saved and signed off", "Appeal deadline scheduled"},
		Steps: []Step{
			{Key: "denial", Title: "Read the denial", Tools: tl([]string{"legal_deadline_calc"}, remind), Instructions: "Find the denial letter. Extract the reason, the policy provision cited, and the appeal deadline (compute it). Schedule reminders."},
			{Key: "necessity", Title: "Medical necessity", Deps: []string{"denial"}, Tools: tl(clinical, drafting), Instructions: "Draft a letter of medical necessity citing clinical guidelines (PubMed). Save it.", Verify: []string{"document_saved"}},
			{Key: "policy", Title: "Policy and regulation", Deps: []string{"denial"}, Tools: tl(research, drafting), Instructions: "Research the policy language and applicable law (ERISA or state insurance code). Save a memo.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "physician", Title: "Physician signs the necessity letter", Deps: []string{"necessity"}, Tools: tl(signoffA), Instructions: "Request physician sign-off.", Verify: []string{"signed_off"}},
			{Key: "packet", Title: "Assemble and approve the appeal", Deps: []string{"physician", "policy"}, Tools: tl(drafting, signoffA), Instructions: "Assemble the appeal letter and packet. Save it and request attorney sign-off.", Verify: []string{"document_saved", "signed_off"}},
		}})

	add(&Skill{Name: "workers-comp-claim", Domain: "medlegal", Title: "Workers' compensation claim", TitleZH: "工伤索赔",
		Description: "Injury intake, treatment timeline, forms, deadlines.",
		Criteria:    []string{"Claim packet saved and signed off", "Deadlines scheduled"},
		Steps: []Step{
			{Key: "intake", Title: "Injury and employer details", Tools: tl([]string{"legal_deadline_calc"}, remind), Instructions: "Record the injury, date, employer, reporting status. Compute the notice and filing deadlines for the state. Schedule reminders."},
			{Key: "medical", Title: "Treatment timeline", Deps: []string{"intake"}, Tools: tl(clinical, drafting), Instructions: "Summarise treatment and work restrictions. Save.", Verify: []string{"document_saved"}},
			{Key: "packet", Title: "Claim packet", Deps: []string{"medical"}, Tools: tl(research, drafting, signoffA), Instructions: "Identify the state forms and assemble the packet. Save and request attorney sign-off.", Verify: []string{"document_saved", "signed_off"}},
		}})

	add(&Skill{Name: "malpractice-screen", Domain: "medlegal", Title: "Medical malpractice screen", TitleZH: "医疗过失初筛",
		Description: "Standard of care, causation, limitation period, attorney handoff.",
		Criteria:    []string{"Screening memo saved", "Limitation deadline scheduled", "Attorney has reviewed"},
		Steps: []Step{
			{Key: "records", Title: "What happened, clinically", Tools: tl(clinical, drafting), Instructions: "Summarise the care and outcome from records. Save.", Verify: []string{"document_saved"}},
			{Key: "sol", Title: "Limitation period", Tools: tl([]string{"legal_deadline_calc"}, research, remind), Instructions: "Find the malpractice limitation and any notice-of-intent rule in the state. Compute and schedule."},
			{Key: "screen", Title: "Standard of care screen", Deps: []string{"records"}, Tools: tl(clinical, research, drafting), Instructions: "Assess whether care plausibly fell below the standard, causation, and damages. State whether an expert review is needed. Save memo.", Verify: []string{"document_saved", "citations_verified"}},
			{Key: "review", Title: "Attorney review", Deps: []string{"screen", "sol"}, Tools: tl(signoffA), Instructions: "Request attorney sign-off.", Verify: []string{"signed_off"}},
		}})

	add(&Skill{Name: "records-request", Domain: "medlegal", Title: "Request medical records", TitleZH: "调取病历",
		Description: "HIPAA authorisation, send, wait 30 days, follow up.",
		Criteria:    []string{"Request sent", "Records received or follow-up sent after 30 days"},
		Steps: []Step{
			{Key: "draft", Title: "Draft the HIPAA request", Tools: tl(drafting), Instructions: "Draft a HIPAA right-of-access request (45 CFR 164.524) from the client to the provider. Save as letter.", Verify: []string{"document_saved"}},
			{Key: "send", Title: "Send it", Deps: []string{"draft"}, Tools: tl([]string{"email_send"}, remind), Instructions: "Send with on_behalf_of='client' if the provider's records email is known; otherwise deliver to the client. Schedule a reminder at 30 days."},
			{Key: "wait", Title: "Wait for the provider (30 days)", Deps: []string{"send"}, WaitDays: 30},
			{Key: "followup", Title: "Follow up", Deps: []string{"wait"}, Tools: tl([]string{"email_send"}, drafting), Instructions: "Check whether records arrived (kb_search). If not, draft and send a follow-up citing the 30-day requirement."},
		}})

	add(&Skill{Name: "disability-claim", Domain: "medlegal", Title: "Disability claim", TitleZH: "残障福利申请",
		Description: "Medical evidence, functional limitations, forms.",
		Criteria:    []string{"Claim packet saved and signed off"},
		Steps: []Step{
			{Key: "evidence", Title: "Medical evidence", Tools: tl(clinical, drafting), Instructions: "Summarise diagnoses, treatment and functional limitations from records. Save.", Verify: []string{"document_saved"}},
			{Key: "packet", Title: "Claim packet", Deps: []string{"evidence"}, Tools: tl(research, drafting, signoffA), Instructions: "Map limitations to program criteria and assemble the packet. Save and request attorney sign-off.", Verify: []string{"document_saved", "signed_off"}},
		}})

	// ── General fallback ────────────────────────────────────────────────────
	add(&Skill{Name: "general-task", Domain: "general", Title: "General task", TitleZH: "通用任务",
		Description: "Any multi-step request that does not fit a specific playbook.",
		Criteria:    []string{"The client's request is fulfilled and the result saved"},
		Steps: []Step{
			{Key: "work", Title: "Do the work", Tools: tl(research, drafting, clinical, remind), Instructions: "Complete the client's request. Save the result.", Verify: []string{"document_saved"}, MaxSteps: 12},
			{Key: "report", Title: "Report back", Deps: []string{"work"}, Tools: tl(), Instructions: "notify_user with the result."},
		}})
}
