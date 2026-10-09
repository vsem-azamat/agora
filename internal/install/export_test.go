package install

// BeforeWrite runs f before each settings file is checked for changes and written, and returns a
// function that removes it.
func BeforeWrite(f func()) func() {
	beforeWrite = f
	return func() { beforeWrite = nil }
}
