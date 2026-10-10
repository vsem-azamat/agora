package queue

// Holders returns the entries of r that hold a slot, for tests.
func Holders(r Resource) []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.State == Held {
			out = append(out, e)
		}
	}
	return out
}
