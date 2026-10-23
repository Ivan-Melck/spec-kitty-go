package doctrine

// IdentityIndex resolves either a file stem or a declared id. Stem matches
// across all layers precede declared-id matches, with project precedence.
type IdentityIndex struct {
	root   string
	tables map[Kind]*identityTable
}

type identityEntry struct{ stem, id, layer string }
type identityTable struct {
	stems, ids map[string]identityEntry
	entries    []identityEntry
}

func NewIdentityIndex(root string) *IdentityIndex {
	return &IdentityIndex{root: root, tables: map[Kind]*identityTable{}}
}

func (ix *IdentityIndex) table(k Kind) *identityTable {
	if t := ix.tables[k]; t != nil {
		return t
	}
	t := &identityTable{stems: map[string]identityEntry{}, ids: map[string]identityEntry{}}
	dirs := scanDirs(k.OperatorToken(), LayerRoots(ix.root), OrgRoots(ix.root))
	ordered := make([]scanDir, 0, len(dirs))
	for _, layer := range []string{LayerProject, LayerOrg, LayerBuiltIn} {
		for _, sd := range dirs {
			if sd.layer == layer {
				ordered = append(ordered, sd)
			}
		}
	}
	for _, sd := range ordered {
		for _, rel := range sd.pack.Walk(sd.dir, k.Suffix(), true) {
			b, err := sd.pack.ReadFile(rel)
			if err != nil {
				continue
			}
			id := declaredID(b, k)
			if id == "" {
				continue
			}
			e := identityEntry{ConfigStem(rel), id, sd.layer}
			t.entries = append(t.entries, e)
			if _, ok := t.stems[e.stem]; !ok {
				t.stems[e.stem] = e
			}
			if _, ok := t.ids[e.id]; !ok {
				t.ids[e.id] = e
			}
		}
	}
	ix.tables[k] = t
	return t
}

func (ix *IdentityIndex) DeclaredIDInLayer(k Kind, stem, layer string) string {
	for _, e := range ix.table(k).entries {
		if e.stem == stem && e.layer == layer {
			return e.id
		}
	}
	return ix.DeclaredID(k, stem)
}

func (ix *IdentityIndex) entry(k Kind, token string) (identityEntry, bool) {
	t := ix.table(k)
	if e, ok := t.stems[token]; ok {
		return e, true
	}
	if e, ok := t.ids[token]; ok {
		return e, true
	}
	if k == Directive {
		e, ok := t.ids[NormalizeDirectiveID(token)]
		return e, ok
	}
	return identityEntry{}, false
}

func (ix *IdentityIndex) Stem(k Kind, token string) string {
	if e, ok := ix.entry(k, token); ok {
		return e.stem
	}
	return token
}

func (ix *IdentityIndex) DeclaredID(k Kind, token string) string {
	if e, ok := ix.entry(k, token); ok {
		return e.id
	}
	return ""
}

// Spellings retains unknown entries so a stale exclusion is never discarded.
func (ix *IdentityIndex) Spellings(k Kind, token string) IDSet {
	if e, ok := ix.entry(k, token); ok {
		return IDSet{e.stem: true, e.id: true, string(k) + ":" + e.id: true}
	}
	return IDSet{token: true, string(k) + ":" + token: true}
}

func EffectiveDeny(k Kind, stored, required []string, ix *IdentityIndex) IDSet {
	floor := IDSet{}
	for _, token := range required {
		for s := range ix.Spellings(k, token) {
			floor[s] = true
		}
	}
	out := IDSet{}
	for _, token := range stored {
		spellings, mandatory := ix.Spellings(k, token), false
		for s := range spellings {
			mandatory = mandatory || floor[s]
		}
		if !mandatory {
			for s := range spellings {
				out[s] = true
			}
		}
	}
	return out
}
