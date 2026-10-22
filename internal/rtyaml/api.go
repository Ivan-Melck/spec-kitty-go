package rtyaml

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// Load is `yaml = YAML(); yaml.preserve_quotes = True; yaml.load(in)`:
// the document as commented containers (*Map, *Seq) and round-trip
// scalars, nil for an empty stream. Errors are ruamel's exact load errors
// (*pyyaml.MarkedYAMLError, *pyyaml.ReaderError, *pyyaml.PyError).
func Load(in pyyaml.Input) (any, error) {
	if _, err := pyyaml.LoadRuamelInput(in, pyyaml.Ruamel); err != nil {
		return nil, err
	}
	n, err := pyyaml.ComposeInput(in, pyyaml.ComposeOptions{Dialect: pyyaml.Ruamel})
	if err != nil || n == nil {
		return nil, err
	}
	c := &constructor{built: map[*pyyaml.Node]any{}}
	return c.object(n), nil
}

// LoadString is Load of a Python str.
func LoadString(src string) (any, error) { return Load(pyyaml.StringInput(src)) }

// Dump is `yaml.dump(data, stream)` with a default YAML() instance: the
// round-trip representer, serializer and emitter (block style, indent 2,
// width 80, allow_unicode). data is what Load returns, possibly edited,
// with plain Go values (string, bool, int, int64, *big.Int, float64, nil,
// []any, *jsonx.Object) mixed in as Python's plain types.
func Dump(data any) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			ee, ok := r.(*EmitterError)
			if !ok {
				panic(r)
			}
			err = ee
		}
	}()
	em := newEmitter()
	s := &serializer{em: em, nodes: map[*dnode]bool{}, anchors: map[*dnode]*string{}}
	rep := &representer{objects: map[any]*dnode{}}
	em.emit(&eevent{kind: evStreamStart})
	n := rep.data(data)
	s.serialize(n)
	em.emit(&eevent{kind: evStreamEnd})
	if len(em.events) != 0 {
		return "", fmt.Errorf("rtyaml: unflushed events")
	}
	return em.out.String(), nil
}
