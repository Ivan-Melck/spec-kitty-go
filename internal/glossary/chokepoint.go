// Package glossary implements the glossary seed store (.kittify/glossaries),
// term normalisation, the inline dispatch chokepoint scan and the glossary
// event log (.kittify/events/glossary/*.events.jsonl).
package glossary

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Scopes in resolution order.
const (
	ScopeMissionLocal   = "mission_local"
	ScopeTeamDomain     = "team_domain"
	ScopeAudienceDomain = "audience_domain"
	ScopeSpecKittyCore  = "spec_kitty_core"
)

// ScopeOrder is SCOPE_RESOLUTION_ORDER.
var ScopeOrder = []string{ScopeMissionLocal, ScopeTeamDomain, ScopeAudienceDomain, ScopeSpecKittyCore}

// CommonWords is extraction.COMMON_WORDS.
var CommonWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a about after all also an and any as at back be because but by can come could day do even first for from get give go good have he her him his how i if in into it its just know like look make me most my new no not now of on one only or other our out over people say see she so some take than that the their them then there these they think this time to two up us use want way we well what when which who will with work would year you your") {
		CommonWords[w] = true
	}
}

var suffixRules = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`ments$`), ""}, {regexp.MustCompile(`ment$`), ""}, {regexp.MustCompile(`tions$`), ""},
	{regexp.MustCompile(`tion$`), ""}, {regexp.MustCompile(`ness$`), ""}, {regexp.MustCompile(`ings$`), ""},
	{regexp.MustCompile(`ing$`), ""}, {regexp.MustCompile(`ers$`), ""}, {regexp.MustCompile(`ed$`), ""},
	{regexp.MustCompile(`er$`), ""}, {regexp.MustCompile(`s$`), ""},
}

func runeLen(s string) int { return len([]rune(s)) }

// Normalize is the lightweight lemmatizer (drg_builder._normalize).
func Normalize(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, r := range suffixRules {
		c := r.re.ReplaceAllString(token, r.rep)
		if c != token && runeLen(c) >= 3 {
			return c
		}
	}
	return token
}

// URN is glossary_urn: "glossary:" + first 8 hex of sha256(surface).
func URN(surface string) string {
	h := sha256.Sum256([]byte(surface))
	return "glossary:" + hex.EncodeToString(h[:])[:8]
}

// Tokens splits like re.split(r"[\s\W]+", text) (empty pieces kept out).
func Tokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.IsMark(r))
	})
}

// Sense is one seed term sense.
type Sense struct {
	Surface, Scope, Definition, Status string
	Confidence                         float64
}

// SeedPath is .kittify/glossaries/<scope>.yaml.
func SeedPath(root, scope string) string {
	return filepath.Join(root, ".kittify", "glossaries", scope+".yaml")
}

// LoadSeed reads one scope seed file (nil when absent or invalid).
func LoadSeed(root, scope string) []Sense {
	b, err := os.ReadFile(SeedPath(root, scope))
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil
	}
	var out []Sense
	for _, t := range o.GetList("terms") {
		to, ok := t.(*jsonx.Object)
		if !ok {
			continue
		}
		s := Sense{Surface: to.GetString("surface"), Scope: scope, Definition: to.GetString("definition"), Status: to.GetString("status"), Confidence: 1.0}
		if s.Status == "" || (s.Status != "active" && s.Status != "deprecated") {
			if s.Status != "active" {
				s.Status = map[string]string{"deprecated": "deprecated"}[s.Status]
				if s.Status == "" {
					s.Status = "draft"
				}
			}
		}
		if v, ok := to.Get("confidence"); ok {
			switch n := v.(type) {
			case float64:
				s.Confidence = n
			case int:
				s.Confidence = float64(n)
			}
		}
		out = append(out, s)
	}
	return out
}

// Index maps surfaces (and normalised aliases) to active senses.
type Index struct {
	SurfaceToURN    map[string]string
	SurfaceToSenses map[string][]Sense
}

// BuildIndex loads the given scopes and indexes ACTIVE senses.
func BuildIndex(root string, scopes []string) *Index {
	ix := &Index{SurfaceToURN: map[string]string{}, SurfaceToSenses: map[string][]Sense{}}
	for _, sc := range scopes {
		for _, s := range LoadSeed(root, sc) {
			if s.Status != "active" {
				continue
			}
			canon := s.Surface
			urn := URN(canon)
			for _, key := range []string{canon, Normalize(canon)} {
				if _, ok := ix.SurfaceToURN[key]; !ok {
					ix.SurfaceToURN[key] = urn
				}
				ix.SurfaceToSenses[key] = append(ix.SurfaceToSenses[key], s)
				if key == Normalize(canon) && key == canon {
					break
				}
			}
		}
	}
	return ix
}

// Conflict is a SemanticConflict.
type Conflict struct {
	Term, Type, Severity string
	Confidence           float64
	Senses               []Sense
	Context              string
}

// ToObject serialises like semantic_conflict_to_dict.
func (c Conflict) ToObject() *jsonx.Object {
	var cs []any
	for _, s := range c.Senses {
		cs = append(cs, jsonx.NewObject("surface", s.Surface, "scope", s.Scope, "definition", s.Definition, "confidence", jsonx.Float(s.Confidence)))
	}
	if cs == nil {
		cs = []any{}
	}
	return jsonx.NewObject("term", jsonx.NewObject("surface_text", c.Term), "conflict_type", c.Type, "severity", c.Severity,
		"confidence", jsonx.Float(c.Confidence), "candidate_senses", cs, "context", c.Context)
}

// Observation is GlossaryObservationBundle.
type Observation struct {
	MatchedURNs   []string
	AllConflicts  []Conflict
	HighSeverity  []Conflict
	TokensChecked int
	DurationMS    float64
	ErrorMsg      *string
}

// ToObject serialises the bundle.
func (o *Observation) ToObject() *jsonx.Object {
	conv := func(cs []Conflict) []any {
		out := []any{}
		for _, c := range cs {
			out = append(out, c.ToObject())
		}
		return out
	}
	var em any
	if o.ErrorMsg != nil {
		em = *o.ErrorMsg
	}
	return jsonx.NewObject("matched_urns", strs(o.MatchedURNs), "high_severity", conv(o.HighSeverity), "all_conflicts", conv(o.AllConflicts),
		"tokens_checked", o.TokensChecked, "duration_ms", jsonx.Float(o.DurationMS), "error_msg", em)
}

func strs(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

// DefaultScopes is DEFAULT_APPLICABLE_SCOPES.
var DefaultScopes = []string{ScopeSpecKittyCore, ScopeTeamDomain}

// Scan is GlossaryChokepoint.run. When invocationID is non-empty, unknown
// tokens are recorded as TermCandidateObserved events.
func Scan(root, text, invocationID, actor string) *Observation {
	t0 := time.Now()
	ix := BuildIndex(root, DefaultScopes)
	seen := map[string]bool{}
	var checked []string
	for _, raw := range Tokens(strings.ToLower(text)) {
		if runeLen(raw) < 3 || CommonWords[raw] {
			continue
		}
		n := Normalize(raw)
		if n == "" || runeLen(n) < 3 || seen[n] {
			continue
		}
		seen[n] = true
		checked = append(checked, n)
	}
	obs := &Observation{MatchedURNs: []string{}}
	for _, n := range checked {
		senses := ix.SurfaceToSenses[n]
		if len(senses) > 0 {
			obs.MatchedURNs = append(obs.MatchedURNs, ix.SurfaceToURN[n])
		}
		if len(senses) == 0 {
			if invocationID != "" {
				EmitTermCandidate(root, n, invocationID, actor)
			}
			continue
		}
		if len(senses) > 1 {
			obs.AllConflicts = append(obs.AllConflicts, Conflict{Term: n, Type: "ambiguous", Severity: "medium", Confidence: 1.0, Senses: senses, Context: "request_text"})
		}
	}
	obs.TokensChecked = len(checked)
	obs.DurationMS = float64(time.Since(t0).Microseconds()) / 1000.0
	return obs
}

var unsafeID = regexp.MustCompile(`[^a-zA-Z0-9_\-.]`)

// EventLogPath is .kittify/events/glossary/<mission>.events.jsonl.
func EventLogPath(root, missionID string) string {
	return filepath.Join(root, ".kittify", "events", "glossary", unsafeID.ReplaceAllString(missionID, "-")+".events.jsonl")
}

// EmitTermCandidate records a TermCandidateObserved event for a profile
// invocation.
func EmitTermCandidate(root, term, invocationID, actor string) {
	mission := "profile-invocation-" + invocationID
	ev := jsonx.NewObject("event_type", "TermCandidateObserved", "term", term, "source_step", "profile-invocation:"+invocationID,
		"actor_id", actor, "confidence", jsonx.Float(1.0), "extraction_method", "request_text", "context", "source: request_text",
		"mission_id", mission, "run_id", invocationID, "timestamp", clock.NowUTCISO(), "event_id", newUUID())
	AppendEvent(root, mission, ev)
}

// newUUID returns a random RFC 4122 version 4 UUID (Python str(uuid.uuid4())).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
