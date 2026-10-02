package tools

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/mail"
)

func init() {
	register(&Tool{
		Name:        "med_red_flag_check",
		Description: "Deterministic emergency screen over a symptom description. Run it first on any new symptom report. If it returns emergency=true, stop and escalate.",
		Input:       `{"type":"object","required":["text"],"additionalProperties":false,"properties":{"text":{"type":"string","minLength":1}}}`,
		Output:      `{"type":"object","required":["emergency","flags"]}`,
		Effect:      Read, Gate: G0, Timeout: time.Second,
		Run: func(_ context.Context, _ *Env, a map[string]any) (map[string]any, error) {
			flags := []any{}
			for _, f := range CheckRedFlags(str(a, "text")) {
				flags = append(flags, map[string]any{"code": f.Code, "category": f.Category})
			}
			return map[string]any{"emergency": len(flags) > 0, "flags": flags}, nil
		},
	})

	register(&Tool{
		Name:        "med_icd10_lookup",
		Description: "Look up ICD-10-CM codes by condition name or code (NLM Clinical Tables).",
		Input:       `{"type":"object","required":["terms"],"additionalProperties":false,"properties":{"terms":{"type":"string","minLength":2,"maxLength":120}}}`,
		Output:      `{"type":"object","required":["codes"],"properties":{"codes":{"type":"array"}}}`,
		Effect:      Read, Gate: G0, Timeout: 15 * time.Second, MaxRetries: 2,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			var raw []any
			u := "https://clinicaltables.nlm.nih.gov/api/icd10cm/v3/search?sf=code,name&maxList=10&terms=" + url.QueryEscape(str(a, "terms"))
			if err := getJSON(ctx, env, u, nil, &raw); err != nil {
				return nil, err
			}
			codes := []any{}
			if len(raw) >= 4 {
				if rows, ok := raw[3].([]any); ok {
					for _, r := range rows {
						if pair, ok := r.([]any); ok && len(pair) == 2 {
							codes = append(codes, map[string]any{"code": pair[0], "name": pair[1]})
						}
					}
				}
			}
			return map[string]any{"codes": codes}, nil
		},
	})

	register(&Tool{
		Name:        "med_drug_normalize",
		Description: "Normalise a drug name (brand or generic) to its RxNorm concept (RxCUI) and generic ingredient name.",
		Input:       `{"type":"object","required":["name"],"additionalProperties":false,"properties":{"name":{"type":"string","minLength":2,"maxLength":80}}}`,
		Output:      `{"type":"object","required":["name","found"]}`,
		Effect:      Read, Gate: G0, Timeout: 15 * time.Second, MaxRetries: 2,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			return normalizeDrug(ctx, env, str(a, "name"))
		},
	})

	register(&Tool{
		Name:        "med_drug_label",
		Description: "Fetch the FDA label for a drug: boxed warning, indications, dosage, contraindications, warnings and interactions (openFDA).",
		Input:       `{"type":"object","required":["name"],"additionalProperties":false,"properties":{"name":{"type":"string","minLength":2,"maxLength":80}}}`,
		Output:      `{"type":"object","required":["name","found"]}`,
		Effect:      Read, Gate: G0, Timeout: 20 * time.Second, MaxRetries: 2,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			l, err := drugLabel(ctx, env, str(a, "name"))
			if err != nil {
				return nil, err
			}
			if l == nil {
				return map[string]any{"name": str(a, "name"), "found": false}, nil
			}
			out := map[string]any{"name": str(a, "name"), "found": true}
			for k, v := range l {
				out[k] = truncate(v, 1800)
			}
			return out, nil
		},
	})

	register(&Tool{
		Name: "med_interaction_check",
		Description: "Check a medication list for interactions: each drug's FDA label interaction section is searched for the others, plus a curated table of high-risk pairs. " +
			"Run this on every medication list and before any plan that adds a drug.",
		Input:  `{"type":"object","required":["drugs"],"additionalProperties":false,"properties":{"drugs":{"type":"array","minItems":2,"maxItems":20,"items":{"type":"string","minLength":2}}}}`,
		Output: `{"type":"object","required":["interactions","checked"],"properties":{"interactions":{"type":"array"},"checked":{"type":"array"}}}`,
		Effect: Read, Gate: G0, Timeout: 60 * time.Second, MaxRetries: 1,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			drugs := strs(a, "drugs")
			generic := make([]string, len(drugs))
			for i, d := range drugs {
				generic[i] = strings.ToLower(d)
				if n, err := normalizeDrug(ctx, env, d); err == nil && n["found"] == true {
					generic[i] = strings.ToLower(fmt.Sprint(n["generic"]))
				}
			}
			found := []any{}
			seen := map[string]bool{}
			add := func(a, b, sev, src, detail string) {
				k := a + "|" + b
				if a > b {
					k = b + "|" + a
				}
				if seen[k+src] {
					return
				}
				seen[k+src] = true
				found = append(found, map[string]any{"a": a, "b": b, "severity": sev, "source": src, "detail": detail})
			}
			for i := range generic {
				for j := i + 1; j < len(generic); j++ {
					if sev, why, ok := curatedPair(generic[i], generic[j]); ok {
						add(generic[i], generic[j], sev, "curated high-risk table", why)
					}
				}
			}
			for i, g := range generic {
				l, err := drugLabel(ctx, env, g)
				if err != nil || l == nil {
					continue
				}
				section := strings.ToLower(l["drug_interactions"] + " " + l["warnings"])
				for j, other := range generic {
					if i == j || len(other) < 4 {
						continue
					}
					if idx := strings.Index(section, other); idx >= 0 {
						lo, hi := max(0, idx-160), min(len(section), idx+220)
						add(g, other, "see label", "FDA label of "+g, strings.TrimSpace(section[lo:hi]))
					}
				}
			}
			return map[string]any{"interactions": found, "checked": generic}, nil
		},
	})

	register(&Tool{
		Name:        "med_dose_calc",
		Description: "Deterministic clinical calculators: BMI, Cockcroft-Gault creatinine clearance, and weight-based dose with a maximum cap. Use instead of mental arithmetic.",
		Input: `{"type":"object","required":["calc"],"additionalProperties":false,"properties":{
			"calc":{"type":"string","enum":["bmi","crcl","weight_dose"]},
			"weight_kg":{"type":"number","exclusiveMinimum":0,"maximum":400},"height_cm":{"type":"number","exclusiveMinimum":0,"maximum":260},
			"age":{"type":"number","minimum":0,"maximum":120},"sex":{"type":"string","enum":["male","female"]},
			"serum_creatinine_mg_dl":{"type":"number","exclusiveMinimum":0,"maximum":30},
			"mg_per_kg":{"type":"number","exclusiveMinimum":0},"max_mg":{"type":"number","exclusiveMinimum":0}}}`,
		Output: `{"type":"object","required":["calc","value","unit"]}`,
		Effect: Read, Gate: G0, Timeout: time.Second,
		Run: func(_ context.Context, _ *Env, a map[string]any) (map[string]any, error) {
			w := num(a, "weight_kg")
			switch str(a, "calc") {
			case "bmi":
				h := num(a, "height_cm") / 100
				if w == 0 || h == 0 {
					return nil, &InvalidInput{fmt.Errorf("bmi needs weight_kg and height_cm")}
				}
				v := round1(w / (h * h))
				return map[string]any{"calc": "bmi", "value": v, "unit": "kg/m²"}, nil
			case "crcl":
				age, scr := num(a, "age"), num(a, "serum_creatinine_mg_dl")
				if w == 0 || age == 0 || scr == 0 || str(a, "sex") == "" {
					return nil, &InvalidInput{fmt.Errorf("crcl needs weight_kg, age, sex, serum_creatinine_mg_dl")}
				}
				v := (140 - age) * w / (72 * scr)
				if str(a, "sex") == "female" {
					v *= 0.85
				}
				return map[string]any{"calc": "crcl", "value": round1(v), "unit": "mL/min", "method": "Cockcroft-Gault, actual body weight"}, nil
			case "weight_dose":
				mgkg := num(a, "mg_per_kg")
				if w == 0 || mgkg == 0 {
					return nil, &InvalidInput{fmt.Errorf("weight_dose needs weight_kg and mg_per_kg")}
				}
				v, capped := w*mgkg, false
				if m := num(a, "max_mg"); m > 0 && v > m {
					v, capped = m, true
				}
				return map[string]any{"calc": "weight_dose", "value": round1(v), "unit": "mg", "capped": capped}, nil
			}
			return nil, &InvalidInput{fmt.Errorf("unknown calc")}
		},
	})

	register(&Tool{
		Name:        "med_pubmed_search",
		Description: "Search PubMed for clinical evidence. Returns titles, journals, years and PMIDs. Prefer guidelines, systematic reviews and RCTs.",
		Input:       `{"type":"object","required":["query"],"additionalProperties":false,"properties":{"query":{"type":"string","minLength":3,"maxLength":300},"limit":{"type":"integer","minimum":1,"maximum":10}}}`,
		Output:      `{"type":"object","required":["articles"],"properties":{"articles":{"type":"array"}}}`,
		Effect:      Read, Gate: G0, Timeout: 20 * time.Second, MaxRetries: 2,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			limit := int(num(a, "limit"))
			if limit == 0 {
				limit = 5
			}
			var s struct {
				R struct {
					IDs []string `json:"idlist"`
				} `json:"esearchresult"`
			}
			q := url.Values{"db": {"pubmed"}, "term": {str(a, "query")}, "retmode": {"json"}, "retmax": {fmt.Sprint(limit)}, "sort": {"relevance"}}
			if err := getJSON(ctx, env, "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?"+q.Encode(), nil, &s); err != nil {
				return nil, err
			}
			arts := []any{}
			if len(s.R.IDs) == 0 {
				return map[string]any{"articles": arts}, nil
			}
			var sum struct {
				Result map[string]any `json:"result"`
			}
			q2 := url.Values{"db": {"pubmed"}, "id": {strings.Join(s.R.IDs, ",")}, "retmode": {"json"}}
			if err := getJSON(ctx, env, "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esummary.fcgi?"+q2.Encode(), nil, &sum); err != nil {
				return nil, err
			}
			for _, id := range s.R.IDs {
				if r, ok := sum.Result[id].(map[string]any); ok {
					arts = append(arts, map[string]any{"pmid": id, "title": r["title"], "journal": r["fulljournalname"],
						"date": r["pubdate"], "url": "https://pubmed.ncbi.nlm.nih.gov/" + id + "/"})
				}
			}
			return map[string]any{"articles": arts}, nil
		},
	})

	// ── Actions that need a licensed physician ──────────────────────────────

	physicianAction := func(name, desc, input, verb string, idem func(*Env, map[string]any) string, gate func(map[string]any) (Gate, string)) {
		register(&Tool{
			Name: name, Description: desc + " SANDBOX adapter until a live integration is contracted.",
			Input: input, Output: `{"type":"object","required":["reference","status","sandbox"]}`,
			Effect: External, Gate: G2, Role: "physician", GateFor: gate, Timeout: 30 * time.Second,
			IdemKey: idem,
			Preview: func(a map[string]any) string { return verb + ": " + previewArgs(a) },
			Run: func(_ context.Context, env *Env, a map[string]any) (map[string]any, error) {
				ref := "SBX-" + strings.ToUpper(idem(env, a)[len(name)+1:][:10])
				return map[string]any{"reference": ref, "status": "transmitted", "sandbox": true,
					"note": "Sandbox. Nothing was transmitted to a pharmacy, lab or provider."}, nil
			},
			Verify: func(_ context.Context, _ *Env, _, out map[string]any) error {
				if str(out, "status") != "transmitted" {
					return fmt.Errorf("status %q", str(out, "status"))
				}
				return nil
			},
		})
	}
	physicianAction("rx_submit",
		"Send an electronic prescription. A licensed physician must approve this exact prescription. Controlled substances are never automated.",
		`{"type":"object","required":["drug","dose","route","frequency","quantity","refills"],"additionalProperties":false,"properties":{
			"drug":{"type":"string","minLength":2},"dose":{"type":"string"},"route":{"type":"string"},"frequency":{"type":"string"},
			"quantity":{"type":"integer","minimum":1,"maximum":365},"refills":{"type":"integer","minimum":0,"maximum":11},
			"controlled_schedule":{"type":"string","enum":["none","II","III","IV","V"]},"pharmacy":{"type":"string"},"indication":{"type":"string"}}}`,
		"Prescribe",
		func(env *Env, a map[string]any) string {
			return Key(env, "rx_submit", strings.ToLower(str(a, "drug")), str(a, "dose"), str(a, "frequency"))
		},
		func(a map[string]any) (Gate, string) {
			if s := str(a, "controlled_schedule"); (s != "" && s != "none") || IsControlled(str(a, "drug")) {
				return G3, ""
			}
			return G2, "physician"
		})
	physicianAction("lab_order",
		"Order laboratory tests or imaging. A licensed physician must approve this exact order.",
		`{"type":"object","required":["tests","indication"],"additionalProperties":false,"properties":{
			"tests":{"type":"array","minItems":1,"items":{"type":"string"}},"indication":{"type":"string","minLength":3},"priority":{"type":"string","enum":["routine","urgent"]}}}`,
		"Order",
		func(env *Env, a map[string]any) string { return Key(env, "lab_order", strs(a, "tests")) },
		nil)
	physicianAction("referral_send",
		"Refer the patient to a specialist. A licensed physician must approve this exact referral.",
		`{"type":"object","required":["specialty","reason"],"additionalProperties":false,"properties":{
			"specialty":{"type":"string","minLength":3},"reason":{"type":"string","minLength":3},"urgency":{"type":"string","enum":["routine","soon","urgent"]}}}`,
		"Refer",
		func(env *Env, a map[string]any) string { return Key(env, "referral_send", str(a, "specialty")) },
		nil)

	register(&Tool{
		Name: "escalate_emergency",
		Description: "Escalate a medical or safety emergency: records it on the timeline and pages the on-call clinician. Use immediately when red flags are present. " +
			"Never waits for approval.",
		Input:  `{"type":"object","required":["summary","flags"],"additionalProperties":false,"properties":{"summary":{"type":"string","minLength":3},"flags":{"type":"array","items":{"type":"string"}}}}`,
		Output: `{"type":"object","required":["paged"]}`,
		Effect: External, Gate: G0, Timeout: 30 * time.Second, MaxRetries: 2,
		IdemKey: func(env *Env, a map[string]any) string { return Key(env, "escalate_emergency", strs(a, "flags")) },
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			paged := false
			if env.OnCallEmail != "" && env.Mailer != nil {
				_, err := env.Mailer.Send(ctx, mail.Message{
					To:      env.OnCallEmail,
					Subject: "[URGENT] Red flags reported by " + env.UserEmail,
					Text: fmt.Sprintf("Client: %s <%s>\nFlags: %s\n\n%s\n\nThe client has been told to call emergency services.",
						env.UserName, env.UserEmail, strings.Join(strs(a, "flags"), ", "), str(a, "summary")),
					MessageID: Key(env, "page", strs(a, "flags")),
				})
				if err != nil {
					return nil, &Transient{err}
				}
				paged = env.Mailer.Enabled()
			}
			return map[string]any{"paged": paged, "oncall_configured": env.OnCallEmail != ""}, nil
		},
	})
}

func previewArgs(a map[string]any) string {
	var parts []string
	for _, k := range []string{"drug", "dose", "route", "frequency", "quantity", "refills", "tests", "indication", "specialty", "reason", "urgency", "priority"} {
		if v, ok := a[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, ", ")
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func normalizeDrug(ctx context.Context, env *Env, name string) (map[string]any, error) {
	var r struct {
		G struct {
			IDs []string `json:"rxnormId"`
		} `json:"idGroup"`
	}
	if err := getJSON(ctx, env, "https://rxnav.nlm.nih.gov/REST/rxcui.json?search=2&name="+url.QueryEscape(name), nil, &r); err != nil {
		return nil, err
	}
	if len(r.G.IDs) == 0 {
		return map[string]any{"name": name, "found": false}, nil
	}
	cui := r.G.IDs[0]
	generic := name
	var rel struct {
		G struct {
			CG []struct {
				TTY string `json:"tty"`
				CP  []struct {
					Name string `json:"name"`
				} `json:"conceptProperties"`
			} `json:"conceptGroup"`
		} `json:"relatedGroup"`
	}
	if getJSON(ctx, env, "https://rxnav.nlm.nih.gov/REST/rxcui/"+cui+"/related.json?tty=IN", nil, &rel) == nil {
		for _, g := range rel.G.CG {
			if g.TTY == "IN" && len(g.CP) > 0 {
				generic = g.CP[0].Name
			}
		}
	}
	return map[string]any{"name": name, "found": true, "rxcui": cui, "generic": generic}, nil
}

func drugLabel(ctx context.Context, env *Env, name string) (map[string]string, error) {
	q := fmt.Sprintf(`openfda.generic_name:"%s" OR openfda.brand_name:"%s"`, name, name)
	var r struct {
		Results []map[string]any `json:"results"`
	}
	err := getJSON(ctx, env, "https://api.fda.gov/drug/label.json?limit=1&search="+url.QueryEscape(q), nil, &r)
	if err == errNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(r.Results) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, k := range []string{"boxed_warning", "indications_and_usage", "dosage_and_administration", "contraindications", "warnings", "warnings_and_cautions", "drug_interactions", "adverse_reactions"} {
		if arr, ok := r.Results[0][k].([]any); ok && len(arr) > 0 {
			out[k] = fmt.Sprint(arr[0])
		}
	}
	if out["warnings"] == "" {
		out["warnings"] = out["warnings_and_cautions"]
	}
	delete(out, "warnings_and_cautions")
	return out, nil
}

// High-risk pairs that must never depend on label text matching. Generic names.
var curated = []struct{ a, b, sev, why string }{
	{"warfarin", "ibuprofen", "major", "NSAIDs raise bleeding risk with warfarin"},
	{"warfarin", "aspirin", "major", "additive bleeding risk"},
	{"warfarin", "naproxen", "major", "NSAIDs raise bleeding risk with warfarin"},
	{"lisinopril", "spironolactone", "major", "hyperkalaemia"},
	{"lisinopril", "potassium chloride", "major", "hyperkalaemia"},
	{"sildenafil", "nitroglycerin", "contraindicated", "severe hypotension"},
	{"sildenafil", "isosorbide mononitrate", "contraindicated", "severe hypotension"},
	{"simvastatin", "clarithromycin", "contraindicated", "rhabdomyolysis via CYP3A4 inhibition"},
	{"methotrexate", "trimethoprim", "major", "bone-marrow suppression"},
	{"sertraline", "tramadol", "major", "serotonin syndrome and seizure risk"},
	{"fluoxetine", "tramadol", "major", "serotonin syndrome and seizure risk"},
	{"sertraline", "phenelzine", "contraindicated", "serotonin syndrome"},
	{"clopidogrel", "omeprazole", "moderate", "reduced antiplatelet effect"},
	{"lithium", "ibuprofen", "major", "raised lithium levels"},
	{"digoxin", "amiodarone", "major", "digoxin toxicity"},
	{"metformin", "iodinated contrast", "major", "lactic acidosis risk around contrast"},
	{"oxycodone", "alprazolam", "major", "respiratory depression (opioid + benzodiazepine)"},
	{"ibuprofen", "lisinopril", "moderate", "reduced antihypertensive effect; kidney injury risk"},
}

func curatedPair(a, b string) (string, string, bool) {
	for _, c := range curated {
		if (strings.Contains(a, c.a) && strings.Contains(b, c.b)) || (strings.Contains(a, c.b) && strings.Contains(b, c.a)) {
			return c.sev, c.why, true
		}
	}
	return "", "", false
}

var controlled = []string{"oxycodone", "hydrocodone", "morphine", "fentanyl", "codeine", "tramadol", "methadone", "buprenorphine",
	"hydromorphone", "tapentadol", "alprazolam", "lorazepam", "diazepam", "clonazepam", "temazepam", "zolpidem", "eszopiclone",
	"amphetamine", "methylphenidate", "lisdexamfetamine", "dextroamphetamine", "modafinil", "armodafinil", "pregabalin",
	"phenobarbital", "ketamine", "testosterone", "carisoprodol", "adderall", "xanax", "valium", "ambien", "ritalin", "vyvanse", "percocet", "vicodin", "lyrica"}

// IsControlled is a deterministic backstop: the model's own claim that a drug
// is not scheduled is never trusted for this decision.
func IsControlled(drug string) bool {
	d := strings.ToLower(drug)
	for _, c := range controlled {
		if strings.Contains(d, c) {
			return true
		}
	}
	return false
}
