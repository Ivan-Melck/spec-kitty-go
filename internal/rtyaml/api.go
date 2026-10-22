package rtyaml

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// Doc is a loaded document together with the directive state the load
// leaves on the YAML() instance (yaml.version, yaml.tags), which the same
// instance's dump re-emits (%YAML / %TAG directives, YAML 1.1 resolution).
type Doc struct {
	Root any
	Info pyyaml.DocInfo
}

// Load is `yaml = YAML(); yaml.preserve_quotes = True; yaml.load(in)`:
// the document as commented containers (*Map, *Seq) and round-trip
// scalars (Root nil for an empty stream). Errors are ruamel's exact load
// errors (*pyyaml.MarkedYAMLError, *pyyaml.ReaderError, *pyyaml.PyError).
func Load(in pyyaml.Input) (*Doc, error) {
	if _, err := pyyaml.LoadRuamelInput(in, pyyaml.Ruamel); err != nil {
		return nil, err
	}
	n, info, err := pyyaml.ComposeDocument(in, pyyaml.ComposeOptions{Dialect: pyyaml.Ruamel})
	if err != nil {
		return nil, err
	}
	d := &Doc{Info: info}
	if n != nil {
		c := &constructor{built: map[*pyyaml.Node]any{}, v11: info.Version != nil && *info.Version == [2]int{1, 1}}
		d.Root = c.object(n)
	}
	return d, nil
}

// LoadString is Load of a Python str.
func LoadString(src string) (*Doc, error) { return Load(pyyaml.StringInput(src)) }

// Dump is `yaml.dump(d.Root, stream)` on the instance that loaded d.
func (d *Doc) Dump() (string, error) { return dump(d.Root, d.Info, 0) }

// DumpWidth is Dump with `yaml.width = width`.
func (d *Doc) DumpWidth(width int) (string, error) { return dump(d.Root, d.Info, width) }

// DumpWidth is Dump with `yaml.width = width` on a fresh instance.
func DumpWidth(data any, width int) (string, error) { return dump(data, pyyaml.DocInfo{}, width) }

// Dump is `yaml.dump(data, stream)` with a fresh YAML() instance: the
// round-trip representer, serializer and emitter (block style, indent 2,
// width 80, allow_unicode). data is what Load returns, possibly edited,
// with plain Go values (string, bool, int, int64, *big.Int, float64, nil,
// []any, *jsonx.Object) standing for Python's plain types.
func Dump(data any) (string, error) { return dump(data, pyyaml.DocInfo{}, 0) }

func dump(data any, info pyyaml.DocInfo, width int) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			ee, ok := r.(*EmitterError)
			if !ok {
				panic(r)
			}
			err = ee
		}
	}()
	v11 := info.Version != nil && *info.Version == [2]int{1, 1}
	em := newEmitter()
	em.v11 = v11
	if width > em.bestSequenceIndent*2 {
		em.bestWidth = width
	}
	s := &serializer{em: em, nodes: map[*dnode]bool{}, anchors: map[*dnode]*string{}, v11: v11,
		version: info.Version, tags: info.Tags}
	rep := &representer{objects: map[any]*dnode{}, v11: v11}
	em.emit(&eevent{kind: evStreamStart})
	n := rep.data(data)
	s.serialize(n)
	em.emit(&eevent{kind: evStreamEnd})
	if len(em.events) != 0 {
		return "", fmt.Errorf("rtyaml: unflushed events")
	}
	return em.out.String(), nil
}
