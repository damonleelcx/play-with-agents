package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

func init() {
	register(&Tool{
		Name:        "legal_search_cases",
		Description: "Search US court opinions (CourtListener). Use for case law on a legal question. Returns case names, citations, courts, dates and links. Always prefer cases from the client's jurisdiction.",
		Input: `{"type":"object","required":["query"],"additionalProperties":false,"properties":{
			"query":{"type":"string","minLength":3,"maxLength":300},
			"court":{"type":"string","description":"CourtListener court id filter, e.g. 'cal calctapp' or 'scotus'"},
			"filed_after":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},
			"limit":{"type":"integer","minimum":1,"maximum":10}}}`,
		Output: `{"type":"object","required":["count","results"],"properties":{"count":{"type":"integer"},"results":{"type":"array","items":{"type":"object","required":["case_name","url"]}}}}`,
		Effect: Read, Gate: G0, Timeout: 25 * time.Second, MaxRetries: 2,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			q := url.Values{"type": {"o"}, "q": {str(a, "query")}}
			if c := str(a, "court"); c != "" {
				q.Set("court", c)
			}
			if d := str(a, "filed_after"); d != "" {
				q.Set("filed_after", d)
			}
			limit := int(num(a, "limit"))
			if limit == 0 {
				limit = 6
			}
			var res struct {
				Count   int `json:"count"`
				Results []struct {
					CaseName  string   `json:"caseName"`
					Citation  []string `json:"citation"`
					Court     string   `json:"court"`
					DateFiled string   `json:"dateFiled"`
					URL       string   `json:"absolute_url"`
					Opinions  []struct {
						Snippet string `json:"snippet"`
					} `json:"opinions"`
				} `json:"results"`
			}
			if err := getJSON(ctx, env, "https://www.courtlistener.com/api/rest/v4/search/?"+q.Encode(), clHeaders(env), &res); err != nil {
				return nil, err
			}
			out := []any{}
			for i, r := range res.Results {
				if i >= limit {
					break
				}
				snip := ""
				if len(r.Opinions) > 0 {
					snip = truncate(htmlToText(r.Opinions[0].Snippet), 400)
				}
				out = append(out, map[string]any{
					"case_name": r.CaseName, "citations": r.Citation, "court": r.Court,
					"date_filed": r.DateFiled, "url": "https://www.courtlistener.com" + r.URL, "snippet": snip,
				})
			}
			return map[string]any{"count": res.Count, "results": out}, nil
		},
	})

	register(&Tool{
		Name: "legal_verify_citations",
		Description: "Check every case citation in a text against CourtListener. MUST be run on any draft that cites cases before it is saved or shown as final. " +
			"Returns which citations resolved to a real reported case and which did not.",
		Input:  `{"type":"object","required":["text"],"additionalProperties":false,"properties":{"text":{"type":"string","minLength":1,"maxLength":60000}}}`,
		Output: `{"type":"object","required":["checked","verified","unverified","all_verified"],"properties":{"checked":{"type":"integer"},"verified":{"type":"array"},"unverified":{"type":"array"},"all_verified":{"type":"boolean"}}}`,
		Effect: Read, Gate: G0, Timeout: 90 * time.Second, MaxRetries: 1,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			cites := ExtractCitations(str(a, "text"))
			verified, unverified := []any{}, []any{}
			for _, c := range cites {
				q := url.Values{"type": {"o"}, "q": {`citation:("` + c + `")`}}
				var res struct {
					Count   int `json:"count"`
					Results []struct {
						CaseName string `json:"caseName"`
						URL      string `json:"absolute_url"`
					} `json:"results"`
				}
				if err := getJSON(ctx, env, "https://www.courtlistener.com/api/rest/v4/search/?"+q.Encode(), clHeaders(env), &res); err != nil {
					return nil, err
				}
				if res.Count > 0 && len(res.Results) > 0 {
					verified = append(verified, map[string]any{"citation": c, "case_name": res.Results[0].CaseName,
						"url": "https://www.courtlistener.com" + res.Results[0].URL})
				} else {
					unverified = append(unverified, c)
				}
			}
			return map[string]any{"checked": len(cites), "verified": verified, "unverified": unverified, "all_verified": len(unverified) == 0}, nil
		},
	})

	register(&Tool{
		Name:        "legal_statute_lookup",
		Description: "Fetch the text of a US Code section (e.g. title 42 section 1983) from Cornell LII. For state statutes, search cases or knowledge instead.",
		Input: `{"type":"object","required":["title","section"],"additionalProperties":false,"properties":{
			"title":{"type":"integer","minimum":1,"maximum":54},"section":{"type":"string","pattern":"^[0-9A-Za-z\\-\\.]+$"}}}`,
		Output: `{"type":"object","required":["citation","text","url"]}`,
		Effect: Read, Gate: G0, Timeout: 20 * time.Second, MaxRetries: 2,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			u := fmt.Sprintf("https://www.law.cornell.edu/uscode/text/%d/%s", int(num(a, "title")), str(a, "section"))
			body, err := getText(ctx, env, u)
			if err != nil {
				return nil, err
			}
			text := htmlToText(body)
			if i := strings.Index(text, "U.S. Code §"); i > 0 {
				text = text[i:]
			}
			return map[string]any{"citation": fmt.Sprintf("%d U.S.C. § %s", int(num(a, "title")), str(a, "section")), "text": truncate(text, 9000), "url": u}, nil
		},
		Verify: func(_ context.Context, _ *Env, _, out map[string]any) error {
			if len(str(out, "text")) < 80 {
				return fmt.Errorf("the page did not contain statute text")
			}
			return nil
		},
	})

	register(&Tool{
		Name: "legal_deadline_calc",
		Description: "Compute a legal deadline deterministically. Counts from a trigger date by calendar or court days, excluding the trigger day, and rolls a deadline that lands on a weekend or US federal holiday to the next court day (FRCP 6(a) method). " +
			"Use this instead of doing date arithmetic yourself.",
		Input: `{"type":"object","required":["trigger_date","days","count"],"additionalProperties":false,"properties":{
			"trigger_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},
			"days":{"type":"integer","minimum":-3650,"maximum":3650},
			"count":{"type":"string","enum":["calendar","court"]},
			"label":{"type":"string"}}}`,
		Output: `{"type":"object","required":["deadline","weekday","rolled"]}`,
		Effect: Read, Gate: G0, Timeout: time.Second,
		Run: func(_ context.Context, _ *Env, a map[string]any) (map[string]any, error) {
			t, err := time.Parse("2006-01-02", str(a, "trigger_date"))
			if err != nil {
				return nil, err
			}
			d, rolled := Deadline(t, int(num(a, "days")), str(a, "count") == "court")
			return map[string]any{"deadline": d.Format("2006-01-02"), "weekday": d.Weekday().String(), "rolled": rolled,
				"label": str(a, "label"), "method": "FRCP 6(a); US federal holidays; verify against local rules"}, nil
		},
	})

	register(&Tool{
		Name:        "legal_conflict_check",
		Description: "Check whether a party name already appears in this client's other matters, to surface conflicts of interest before work begins.",
		Input:       `{"type":"object","required":["parties"],"additionalProperties":false,"properties":{"parties":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"string","minLength":2}}}}`,
		Output:      `{"type":"object","required":["hits"],"properties":{"hits":{"type":"array"}}}`,
		Effect:      Read, Gate: G0, Timeout: 10 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			hits := []any{}
			for _, p := range strs(a, "parties") {
				rows, err := env.Pool.Query(ctx, `SELECT id, title FROM goals WHERE user_id=$1 AND id <> nullif($2,'')::uuid
					AND (objective ILIKE '%'||$3||'%' OR title ILIKE '%'||$3||'%') LIMIT 5`, env.UserID, env.GoalID, p)
				if err != nil {
					return nil, err
				}
				for rows.Next() {
					var id, title string
					if rows.Scan(&id, &title) == nil {
						hits = append(hits, map[string]any{"party": p, "goal_id": id, "matter": title})
					}
				}
				rows.Close()
			}
			return map[string]any{"hits": hits}, nil
		},
	})

	register(&Tool{
		Name: "court_efile",
		Description: "Submit a finalised, attorney-signed document to a court's e-filing system. Requires a licensed attorney's approval of this exact filing. " +
			"SANDBOX adapter: no live court is contacted until an e-filing service provider account is configured.",
		Input: `{"type":"object","required":["document_id","court","case_number","filing_type"],"additionalProperties":false,"properties":{
			"document_id":{"type":"string","format":"uuid"},"court":{"type":"string","minLength":2},"case_number":{"type":"string"},
			"filing_type":{"type":"string","minLength":2}}}`,
		Output: `{"type":"object","required":["envelope_id","status","sandbox"]}`,
		Effect: External, Gate: G2, Role: "attorney", Timeout: 60 * time.Second,
		IdemKey: func(env *Env, a map[string]any) string {
			return Key(env, "court_efile", str(a, "document_id"), str(a, "case_number"), str(a, "filing_type"))
		},
		Preview: func(a map[string]any) string {
			return fmt.Sprintf("File %q in %s, case %s (document %s)", str(a, "filing_type"), str(a, "court"), str(a, "case_number"), str(a, "document_id"))
		},
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			if err := docBelongs(ctx, env, str(a, "document_id")); err != nil {
				return nil, err
			}
			env_ := "SBX-" + strings.ToUpper(Key(env, "efile", str(a, "document_id"))[12:24])
			return map[string]any{"envelope_id": env_, "status": "accepted", "sandbox": true,
				"note": "Sandbox filing. No court received this document."}, nil
		},
		Verify: func(_ context.Context, _ *Env, _, out map[string]any) error {
			if str(out, "status") != "accepted" && str(out, "status") != "submitted" {
				return fmt.Errorf("filing status %q", str(out, "status"))
			}
			return nil
		},
	})
}

func clHeaders(env *Env) map[string]string {
	if env != nil && env.CourtListener != "" {
		return map[string]string{"Authorization": "Token " + env.CourtListener}
	}
	return nil
}

// reCite matches reporter citations against an explicit list of reporters, so
// dates ("10 Jan. 2024") and statutes are never mistaken for cases. Volume,
// reporter, optional series, page.
var reCite = regexp.MustCompile(`\b(\d{1,4})\s+(` + reporters + `)(?:(\s?)(2d|3d|4th|5th|6th))?\s+(\d{1,5})\b`)

const reporters = `U\.\s?S\.|S\.\s?Ct\.|L\.\s?Ed\.|F\.\s?Supp\.|F\.\s?App'x|F\.|B\.R\.|` +
	`P\.|A\.|N\.E\.|N\.W\.|S\.E\.|S\.W\.|So\.|` +
	`Cal\.\s?Rptr\.|Cal\.\s?App\.|Cal\.|N\.Y\.S\.|N\.Y\.|A\.D\.|Misc\.|` +
	`Ill\.\s?App\.|Ill\.\s?Dec\.|Ill\.|Ohio\s?St\.|Ohio\s?App\.|Mass\.\s?App\.\s?Ct\.|Mass\.|Pa\.\s?Super\.|Pa\.|` +
	`N\.J\.\s?Super\.|N\.J\.|Wash\.\s?App\.|Wash\.|Mich\.\s?App\.|Mich\.|Tex\.|Fla\.|Ga\.\s?App\.|Ga\.|Va\.|Md\.|Conn\.|Or\.|Wis\.|Minn\.|Colo\.|Ariz\.|Nev\.|Haw\.`

// ExtractCitations returns the distinct reporter citations in text.
func ExtractCitations(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range reCite.FindAllStringSubmatch(text, -1) {
		// Keep the series spacing as written: "F.3d" and "Cal. 4th" are both
		// conventional, and the search matches the conventional form.
		rep := strings.Join(strings.Fields(m[2]), " ")
		if m[4] != "" {
			if m[3] != "" {
				rep += " "
			}
			rep += m[4]
		}
		c := m[1] + " " + rep + " " + m[5]
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// Deadline implements the FRCP 6(a)(1) counting method.
func Deadline(trigger time.Time, days int, courtDays bool) (time.Time, bool) {
	step := 1
	if days < 0 {
		step, days = -1, -days
	}
	d := trigger
	for n := 0; n < days; {
		d = d.AddDate(0, 0, step)
		if !courtDays || IsCourtDay(d) {
			n++
		}
	}
	rolled := false
	for !IsCourtDay(d) {
		d = d.AddDate(0, 0, step)
		rolled = true
	}
	return d, rolled
}

func IsCourtDay(d time.Time) bool {
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return false
	}
	return !FederalHoliday(d)
}

// FederalHoliday reports whether d is a US federal holiday (observed).
func FederalHoliday(d time.Time) bool {
	y, m, day := d.Date()
	nth := func(month time.Month, wd time.Weekday, n int) int {
		t := time.Date(y, month, 1, 0, 0, 0, 0, time.UTC)
		for t.Weekday() != wd {
			t = t.AddDate(0, 0, 1)
		}
		return t.AddDate(0, 0, 7*(n-1)).Day()
	}
	last := func(month time.Month, wd time.Weekday) int {
		t := time.Date(y, month+1, 0, 0, 0, 0, 0, time.UTC)
		for t.Weekday() != wd {
			t = t.AddDate(0, 0, -1)
		}
		return t.Day()
	}
	observed := func(month time.Month, dd int) bool {
		t := time.Date(y, month, dd, 0, 0, 0, 0, time.UTC)
		switch t.Weekday() {
		case time.Saturday:
			t = t.AddDate(0, 0, -1)
		case time.Sunday:
			t = t.AddDate(0, 0, 1)
		}
		return t.Month() == m && t.Day() == day
	}
	switch {
	case observed(time.January, 1), observed(time.June, 19), observed(time.July, 4),
		observed(time.November, 11), observed(time.December, 25):
		return true
	case m == time.January && day == nth(time.January, time.Monday, 3),
		m == time.February && day == nth(time.February, time.Monday, 3),
		m == time.May && day == last(time.May, time.Monday),
		m == time.September && day == nth(time.September, time.Monday, 1),
		m == time.October && day == nth(time.October, time.Monday, 2),
		m == time.November && day == nth(time.November, time.Thursday, 4):
		return true
	}
	// New Year's Day of NEXT year observed on Friday Dec 31.
	if m == time.December && day == 31 && d.Weekday() == time.Friday {
		return true
	}
	return false
}
