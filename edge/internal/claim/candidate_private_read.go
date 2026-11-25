package claim

// ReadCandidatePrivateFiles snapshots a fixed list of bounded private files
// from the pinned no-follow store. No writes or network operation occurs.
func ReadCandidatePrivateFiles(dir string, names ...string) (map[string][]byte, error) {
	store, e := candidateOpenStore(dir, false)
	if e != nil {
		return nil, e
	}
	defer store.Close()
	out := map[string][]byte{}
	for _, name := range names {
		b, e := store.Read(name)
		if e != nil {
			return nil, e
		}
		out[name] = b
	}
	return out, nil
}
