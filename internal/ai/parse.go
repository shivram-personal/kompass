package ai

import (
	"encoding/json"
	"regexp"
	"strings"
)

var jsonBlockRe = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")

// diagnosisFromText assembles the Diagnosis from the CLI's final text. The
// prompt asks for a trailing fenced json block {root_cause, remediation,
// confidence}; we parse the last one. Absent that, the whole text is the report
// and its first paragraph the root cause.
func diagnosisFromText(text string) Diagnosis {
	text = strings.TrimSpace(text)
	d := Diagnosis{Report: text}
	if m := jsonBlockRe.FindAllStringSubmatch(text, -1); len(m) > 0 {
		var parsed struct {
			RootCause      string   `json:"root_cause"`
			Remediation    []string `json:"remediation"`
			RecommendedFix string   `json:"recommended_fix"`
			Confidence     *float64 `json:"confidence"`
		}
		if json.Unmarshal([]byte(m[len(m)-1][1]), &parsed) == nil {
			d.RootCause = parsed.RootCause
			d.Remediation = parsed.Remediation
			d.RecommendedFix = parsed.RecommendedFix
			d.Confidence = parsed.Confidence
			d.Report = strings.TrimSpace(jsonBlockRe.ReplaceAllString(text, ""))
		}
	}
	if d.RootCause == "" {
		d.RootCause = firstParagraph(text)
	}
	return d
}

func firstParagraph(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n\n"); i > 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 600 {
		s = string(r[:600]) + "…"
	}
	return s
}
