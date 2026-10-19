// Package viewengine builds the production HTML view engine for the gateway.
//
// The upstream Fiber html engine (github.com/gofiber/template/html/v3 v3.0.4) is not safe for
// concurrent renders: `Render` copies a shared *template.Template outside any lock
// (html.go:147) and keeps the layout render function it needs for {{embed}} in a shared func
// map (html.go:167), so two in-flight renders can race on html/template's lazy first-execution
// escaping or overwrite each other's layout (the observed "call of nil function").
//
// This package wraps that engine with a single mutex: HTML rendering is serialized for the
// whole application while everything else stays concurrent. Load and Render delegate directly
// to the upstream engine — no buffering, no writer replay, no error swallowing — so the
// semantics of partial writes and returned errors are unchanged.
package viewengine

import (
	"io"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/template/html/v3"

	"core/localization"
)

// innerEngine is the subset of the Fiber html engine this package guards. Keeping it small
// lets the unit tests inject a delegate that observes the adapter's locking.
type innerEngine interface {
	Load() error
	Render(out io.Writer, name string, binding any, layout ...string) error
}

type lockedEngine struct {
	engine innerEngine
	mu     sync.Mutex
}

// NewHTML builds the production HTML engine for the given view directory. It preserves the
// upstream configuration (".html" extension, Reload/Debug driven by development, and the
// `t` translation helper) and returns the serialized wrapper.
func NewHTML(directory string, development bool) fiber.Views {
	engine := html.New(directory, ".html")
	engine.Reload(development)
	engine.Debug(development)
	// t is the explicit-locale translation helper: {{ t .Locale "English source text" }}.
	engine.AddFunc("t", localization.T)
	return &lockedEngine{engine: engine}
}

// Load parses the templates under the same lock as Render, so a live reload can never race a
// request that is already rendering.
func (l *lockedEngine) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.engine.Load()
}

// Render serializes rendering and hands the writer straight to the upstream engine.
func (l *lockedEngine) Render(out io.Writer, name string, binding any, layout ...string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.engine.Render(out, name, binding, layout...)
}

// newWithInner is the test seam: it wraps any delegate with the same serialization.
func newWithInner(inner innerEngine) *lockedEngine {
	return &lockedEngine{engine: inner}
}

// compile-time check that the wrapper satisfies Fiber's view contract.
var _ fiber.Views = (*lockedEngine)(nil)
