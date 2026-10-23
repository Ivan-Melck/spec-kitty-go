package pyyaml

// LoadRuamelInputPy is LoadRuamelInput returning pyyaml.Load's value space
// (Date, Timestamp, []byte, int64 / *big.Int, keys via str()): for callers
// that apply Python type semantics to what ruamel loads from a path.
func LoadRuamelInputPy(in Input, d Dialect) (any, error) {
	v, err := load(in, d)
	if err != nil {
		return nil, err
	}
	return exportPy(v, map[any]any{}), nil
}
