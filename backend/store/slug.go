package store

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// slugLen is the number of random characters in a generated slug. PRD F1.1
// requires an unguessable slug of at least 16 random URL-safe characters —
// possession of the slug is the sole credential for ping endpoints, so this
// must come from crypto/rand, never math/rand.
const slugLen = 16

// generateSlug returns a random, lowercase, URL-safe slug of slugLen
// characters. Base32 (5 bits/char, unpadded) avoids '+', '/', '=' and any
// character that needs URL-escaping.
func generateSlug() (string, error) {
	// ceil(slugLen * 5 bits / 8 bits) bytes of entropy, then trim the
	// base32 encoding down to exactly slugLen characters.
	buf := make([]byte, (slugLen*5+7)/8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("store: generate slug: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return strings.ToLower(enc[:slugLen]), nil
}
