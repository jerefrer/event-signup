package main

import "github.com/microcosm-cc/bluemonday"

// descriptionPolicy is the HTML sanitization policy for event descriptions.
// It is sized to match Trix's output set (https://trix-editor.org/): block
// elements (p, h1, blockquote, pre, ul, ol, li), inline emphasis (strong,
// em, del), and links restricted to http/https/mailto. Anything outside the
// allow-list is stripped, so even a paste from a malicious source can only
// store benign HTML.
var descriptionPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "em", "del", "h1", "blockquote", "pre", "ul", "ol", "li")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	return p
}()

// sanitizeEventDescription cleans HTML coming from the admin description
// editor before it is stored. Called on the write path so the stored value
// can be rendered straight to the page and the email as safe HTML.
func sanitizeEventDescription(s string) string {
	return descriptionPolicy.Sanitize(s)
}
