// Package i18n is a small, dependency-free translation bundle for the web
// dashboard: embedded JSON catalogs, language resolution from a cookie or
// Accept-Language header, and lookup helpers usable as html/template funcs.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var localesFS embed.FS

// DefaultLang is used when no supported language can be resolved from the
// request, and as the fallback for keys missing from another catalog.
const DefaultLang = "de"

// Supported lists the language codes with an embedded catalog.
var Supported = []string{"de", "en"}

// Bundle holds the loaded translation catalogs, keyed by language then key.
type Bundle struct {
	catalogs map[string]map[string]string
}

// New loads the embedded locale catalogs. It panics on malformed embedded
// JSON, which can only happen from a programmer error at build time.
func New() *Bundle {
	catalogs := make(map[string]map[string]string, len(Supported))
	for _, lang := range Supported {
		data, err := localesFS.ReadFile(fmt.Sprintf("locales/%s.json", lang))
		if err != nil {
			panic(fmt.Errorf("i18n: read locale %q: %w", lang, err))
		}
		var catalog map[string]string
		if err := json.Unmarshal(data, &catalog); err != nil {
			panic(fmt.Errorf("i18n: parse locale %q: %w", lang, err))
		}
		catalogs[lang] = catalog
	}
	return &Bundle{catalogs: catalogs}
}

// T looks up key in lang, falling back to DefaultLang and then to the raw key
// itself when the key is in neither catalog. When args are given the message
// is passed through fmt.Sprintf.
func (b *Bundle) T(lang, key string, args ...any) string {
	msg, ok := b.lookup(lang, key)
	if !ok {
		msg = key
	}
	return format(msg, args)
}

// TOr behaves like T but returns fallback instead of the raw key when key is
// missing from every catalog. Used to surface an unmapped raw code (e.g. a
// session reason) instead of a template key.
func (b *Bundle) TOr(lang, key, fallback string, args ...any) string {
	msg, ok := b.lookup(lang, key)
	if !ok {
		return fallback
	}
	return format(msg, args)
}

// lookup returns the message for lang/key, falling back to DefaultLang.
func (b *Bundle) lookup(lang, key string) (string, bool) {
	if msg, ok := b.catalogs[lang][key]; ok {
		return msg, true
	}
	if lang != DefaultLang {
		if msg, ok := b.catalogs[DefaultLang][key]; ok {
			return msg, true
		}
	}
	return "", false
}

func format(msg string, args []any) string {
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Match picks the first Supported language named in an Accept-Language
// header, matching on the primary subtag (e.g. "en-US" matches "en").
// Returns DefaultLang when the header names none of Supported.
func (b *Bundle) Match(acceptLanguage string) string {
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := strings.TrimSpace(part)
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = tag[:i]
		}
		if i := strings.IndexByte(tag, '-'); i >= 0 {
			tag = tag[:i]
		}
		tag = strings.ToLower(strings.TrimSpace(tag))
		for _, sup := range Supported {
			if tag == sup {
				return sup
			}
		}
	}
	return DefaultLang
}

// Resolve picks the request language: the cookie value when it names a
// Supported language, otherwise the best Accept-Language match.
func (b *Bundle) Resolve(cookie, acceptLanguage string) string {
	for _, sup := range Supported {
		if cookie == sup {
			return cookie
		}
	}
	return b.Match(acceptLanguage)
}
