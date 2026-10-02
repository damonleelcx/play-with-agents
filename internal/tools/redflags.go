package tools

import (
	"regexp"
	"strings"
)

// RedFlag is a deterministic emergency rule. These run BEFORE any model sees
// the message and a model cannot override them: if a person describes a stroke,
// the first thing they read is "call 911", not a differential diagnosis.
type RedFlag struct {
	Code     string
	Category string // emergency | crisis
	pattern  *regexp.Regexp
}

var redFlags = []RedFlag{
	{Code: "chest_pain", Category: "emergency", pattern: rx(`(crushing|severe|tight|pressure|squeez)\w*[^.]{0,30}chest|chest[^.]{0,30}(pain|pressure|tight)[^.]{0,60}(arm|jaw|sweat|breath)|胸(口)?(剧痛|压榨|闷痛|疼).{0,10}(手臂|下巴|出汗|喘)|心绞痛`)},
	{Code: "stroke", Category: "emergency", pattern: rx(`face (is )?droop|slurred speech|(sudden|suddenly)[^.]{0,30}(numb|weak|can't (move|speak)|vision loss|confus)|one side of (my|his|her|their) (body|face)|口角歪斜|说话不清|半身(麻木|无力)|突然.{0,6}(看不见|失明|说不出话)`)},
	{Code: "breathing", Category: "emergency", pattern: rx(`can'?t breathe|cannot breathe|struggling to breathe|turning blue|lips (are )?blue|无法呼吸|喘不上气|呼吸困难.{0,6}(严重|加重)|嘴唇发紫`)},
	{Code: "anaphylaxis", Category: "emergency", pattern: rx(`throat (is )?(closing|swelling)|tongue (is )?swelling|anaphyla|喉咙(肿|发紧)|过敏性休克`)},
	{Code: "bleeding", Category: "emergency", pattern: rx(`(won'?t|will not|can'?t) stop bleeding|bleeding heavily|vomiting blood|coughing (up )?blood|血流不止|大出血|吐血|咯血`)},
	{Code: "unconscious", Category: "emergency", pattern: rx(`unconscious|unresponsive|passed out and|not waking up|seizure (that )?(won'?t|will not) stop|昏迷|叫不醒|抽搐不止`)},
	{Code: "overdose", Category: "emergency", pattern: rx(`overdos|took (too many|a bunch of|the whole bottle)|吃了(一整瓶|很多)药|药物过量`)},
	{Code: "suicide", Category: "crisis", pattern: rx(`kill myself|end (my|it all)|suicid|don'?t want to (live|be alive)|want to die|self[- ]harm|hurt myself|自杀|不想活|想死|结束(自己的)?生命|自残|轻生`)},
	{Code: "violence", Category: "crisis", pattern: rx(`(he|she|they|someone) (is going to|will|threatened to) (kill|hurt) me|in danger right now|家暴|有人要杀我|正在被打`)},
}

func rx(s string) *regexp.Regexp { return regexp.MustCompile(`(?i)` + s) }

// CheckRedFlags returns every rule the text trips.
func CheckRedFlags(text string) []RedFlag {
	t := strings.ToLower(text)
	var out []RedFlag
	for _, f := range redFlags {
		if f.pattern.MatchString(t) {
			out = append(out, f)
		}
	}
	return out
}

// EmergencyText is shown verbatim, before anything a model writes. It says a
// clinician was alerted only when the page was actually delivered.
func EmergencyText(flags []RedFlag, lang string, paged bool) string {
	crisis := false
	for _, f := range flags {
		if f.Category == "crisis" {
			crisis = true
		}
	}
	if lang == "zh" {
		if crisis {
			return "**你的安全最重要。** 如果你有伤害自己的想法或正处于危险中，请立即拨打 **120 / 110**（中国大陆）或 **911**（美国），美国也可拨打或发短信至 **988** 自杀与危机求助热线。我会一直在这里陪你，但现在请先联系能马上到你身边的人。"
		}
		t := "**这听起来可能是紧急情况。** 请立即拨打 **120**（中国大陆）或 **911**（美国），或前往最近的急诊室。不要自行开车。"
		if paged {
			t += "我已通知值班临床医生。"
		}
		return t + "在等待救援时，我可以陪你一步步做。"
	}
	if crisis {
		return "**Your safety matters most right now.** If you are thinking about hurting yourself or are in danger, call or text **988** (Suicide & Crisis Lifeline, US) or call **911**. Outside the US, call your local emergency number. I'm here with you, but please reach someone who can be with you now."
	}
	t := "**This may be a medical emergency.** Call **911** (or your local emergency number) now, or go to the nearest emergency department. Don't drive yourself. "
	if paged {
		t += "I've alerted the on-call clinician. "
	}
	return t + "I can stay with you while you wait."
}
