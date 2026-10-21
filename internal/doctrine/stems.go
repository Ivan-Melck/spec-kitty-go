package doctrine

// StemsByDeclaredID maps each declared artifact id of kind k to its config
// stem across the scanned layers (later layers win).
func StemsByDeclaredID(k Kind, layerRoots map[string]string, orgChain []string) map[string]string {
	out := map[string]string{}
	for _, sd := range scanDirs(k.OperatorToken(), layerRoots, orgChain) {
		for _, rel := range sd.pack.Walk(sd.dir, k.Suffix(), true) {
			b, err := sd.pack.ReadFile(rel)
			if err != nil {
				continue
			}
			if id := declaredID(b, k); id != "" {
				out[id] = ConfigStem(rel)
			}
		}
	}
	return out
}

// DeclaredIDForStem is the declared id of the artifact file with config
// stem (later layers win; "" when none).
func DeclaredIDForStem(k Kind, stem string, layerRoots map[string]string, orgChain []string) string {
	found := ""
	for _, sd := range scanDirs(k.OperatorToken(), layerRoots, orgChain) {
		for _, rel := range sd.pack.Walk(sd.dir, k.Suffix(), true) {
			if ConfigStem(rel) != stem {
				continue
			}
			b, err := sd.pack.ReadFile(rel)
			if err != nil {
				continue
			}
			if id := declaredID(b, k); id != "" {
				found = id
			}
		}
	}
	return found
}
