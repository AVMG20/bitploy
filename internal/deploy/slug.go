package deploy

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/ids"
)

// Reserved subdomains (SPEC "Domains & routing"). Anything starting with
// "bp-" is reserved too.
var reservedSlugs = map[string]bool{}

func init() {
	for _, s := range strings.Fields("www app api mcp admin dashboard docs status mail edge custom cdn static assets auth login bitploy traefik") {
		reservedSlugs[s] = true
	}
}

// IsReservedSlug reports whether a slug is on the reserved list or starts with bp-.
func IsReservedSlug(slug string) bool {
	return reservedSlugs[slug] || strings.HasPrefix(slug, "bp-")
}

// ValidateSlug checks the format and reserved words. Uniqueness and the
// 30-day reservation of old slugs are checked by the service against the DB.
// The slug is expected to be lower case already.
func ValidateSlug(slug string) *apperr.Error {
	if len(slug) < 3 || len(slug) > 40 {
		return apperr.BadRequest("slug_invalid",
			"The subdomain "+q(slug)+" must be 3 to 40 characters long.",
			"Use only a-z, 0-9 and -, for example my-cool-app.")
	}
	for _, r := range slug {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return apperr.BadRequest("slug_invalid",
				"The subdomain "+q(slug)+" has a character that is not allowed.",
				"Use only lower case letters a-z, digits 0-9 and -, for example my-cool-app. One level only: no dots.")
		}
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return apperr.BadRequest("slug_invalid",
			"The subdomain "+q(slug)+" cannot start or end with a dash.",
			"Use letters or digits at both ends, for example my-cool-app.")
	}
	if IsReservedSlug(slug) {
		return apperr.Unprocessable("slug_reserved",
			"The subdomain "+q(slug)+" is reserved by bitploy.",
			"Pick another name. Names starting with bp- and words like www, api, admin and mail are reserved.")
	}
	return nil
}

func q(s string) string { return "\"" + s + "\"" }

var adjectives = []string{
	"quiet", "brave", "calm", "clever", "cosmic", "crisp", "eager", "fancy", "gentle", "happy",
	"jolly", "keen", "lively", "lucky", "mellow", "nimble", "proud", "rapid", "shiny", "silent",
	"sunny", "swift", "tidy", "vivid", "witty", "bold", "bright", "cozy", "dandy", "fuzzy",
	"golden", "humble", "icy", "jazzy", "kind", "lunar", "mighty", "noble", "polite", "royal",
}

var nouns = []string{
	"otter", "falcon", "panda", "tiger", "badger", "beaver", "bison", "cobra", "dolphin", "eagle",
	"ferret", "gecko", "heron", "ibis", "jaguar", "koala", "lemur", "lynx", "marmot", "newt",
	"ocelot", "owl", "puffin", "quokka", "raven", "seal", "toucan", "walrus", "yak", "zebra",
	"comet", "meadow", "river", "forest", "harbor", "island", "canyon", "breeze", "pebble", "maple",
}

// GenerateSlug returns a default slug: adjective-noun-xxxx with 4 hex characters.
func GenerateSlug() string {
	return pick(adjectives) + "-" + pick(nouns) + "-" + ids.Hex(4)
}

func pick(list []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		panic(err)
	}
	return list[n.Int64()]
}
