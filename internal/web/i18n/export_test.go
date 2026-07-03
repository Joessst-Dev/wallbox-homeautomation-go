package i18n

// Catalogs exposes the loaded catalogs to black-box tests for the key-parity
// check; production code has no need to enumerate keys.
func Catalogs(b *Bundle) map[string]map[string]string { return b.catalogs }
