package viewengine

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func httptestNewRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, target, nil)
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

// delegateFake records the maximum number of overlapping calls (Load + Render share the lock)
// and can inject errors to prove the lock is released on every path.
type delegateFake struct {
	active    int32
	maxActive int32
	delay     time.Duration
	mu        sync.Mutex
	loadErr   error
	renderErr error
	calls     []string
}

func (f *delegateFake) enter(name string) {
	current := atomic.AddInt32(&f.active, 1)
	for {
		previous := atomic.LoadInt32(&f.maxActive)
		if current <= previous || atomic.CompareAndSwapInt32(&f.maxActive, previous, current) {
			break
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	time.Sleep(f.delay)
}

func (f *delegateFake) exit() { atomic.AddInt32(&f.active, -1) }

func (f *delegateFake) Load() error {
	f.enter("Load")
	defer f.exit()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loadErr
}

func (f *delegateFake) Render(out io.Writer, name string, _ any, _ ...string) error {
	f.enter("Render:" + name)
	defer f.exit()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renderErr != nil {
		return f.renderErr
	}
	_, err := io.WriteString(out, "rendered:"+name)
	return err
}

func TestLoadAndRenderAreMutuallyExclusive(t *testing.T) {
	fake := &delegateFake{delay: time.Millisecond}
	engine := newWithInner(fake)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				if err := engine.Load(); err != nil {
					t.Errorf("Load: %v", err)
				}
				return
			}
			var out strings.Builder
			if err := engine.Render(&out, fmt.Sprintf("view-%d", i), nil); err != nil {
				t.Errorf("Render: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&fake.maxActive); got != 1 {
		t.Fatalf("max concurrent delegate calls = %d, want 1 (Load and Render must share one lock)", got)
	}
	if len(fake.calls) != 8 {
		t.Fatalf("delegate saw %d calls, want 8", len(fake.calls))
	}
}

func TestErrorsAreReturnedVerbatimAndUnlock(t *testing.T) {
	loadFailure := errors.New("load exploded")
	renderFailure := errors.New("render exploded")
	fake := &delegateFake{loadErr: loadFailure, renderErr: renderFailure}
	engine := newWithInner(fake)

	if err := engine.Load(); !errors.Is(err, loadFailure) {
		t.Fatalf("Load error = %v, want the delegate error verbatim", err)
	}
	var out strings.Builder
	if err := engine.Render(&out, "x", nil); !errors.Is(err, renderFailure) {
		t.Fatalf("Render error = %v, want the delegate error verbatim", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a failing delegate must not write output, got %q", out.String())
	}
	// the mutex must be free again: both operations succeed once the delegate stops failing
	fake.mu.Lock()
	fake.loadErr, fake.renderErr = nil, nil
	fake.mu.Unlock()
	if err := engine.Load(); err != nil {
		t.Fatalf("Load after error: %v", err)
	}
	out.Reset()
	if err := engine.Render(&out, "y", nil); err != nil || out.String() != "rendered:y" {
		t.Fatalf("Render after error = (%q, %v)", out.String(), err)
	}
}

func TestRealFactoryErrorsAndRecovery(t *testing.T) {
	engine := NewHTML("./testdata", false)

	var out strings.Builder
	if err := engine.Render(&out, "does-not-exist", nil); err == nil {
		t.Fatal("rendering an unknown template must fail")
	} else if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("unknown-template error lost the name: %v", err)
	}

	// a valid page still renders after the failure (the lock was released)
	out.Reset()
	if err := engine.Render(&out, "page", map[string]any{"Value": "ok"}); err != nil {
		t.Fatalf("valid render after failure: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("valid render produced %q", out.String())
	}

	// a layout with missing templates fails and still releases the lock
	out.Reset()
	if err := engine.Render(&out, "page", nil, "missing-layout"); err == nil {
		t.Fatal("a missing layout must fail")
	}
	out.Reset()
	if err := engine.Render(&out, "page", map[string]any{"Value": "again"}); err != nil {
		t.Fatalf("render after layout failure: %v", err)
	}

	// a failing writer surfaces the write error through the adapter ...
	out.Reset()
	err := engine.Render(failingWriter{}, "page", map[string]any{"Value": "x"})
	if err == nil {
		t.Fatal("a failing writer must surface an error")
	}
	// ... and the lock is released: the next render succeeds (previously unverified)
	out.Reset()
	if err := engine.Render(&out, "page", map[string]any{"Value": "after-writer-failure"}); err != nil {
		t.Fatalf("render after writer failure: %v", err)
	}
	if !strings.Contains(out.String(), "after-writer-failure") {
		t.Fatalf("render after writer failure produced %q", out.String())
	}
}

// partialWriterDelegate writes a prefix to the caller's writer and then fails, proving the
// adapter forwards the writer directly (no buffering/replay) and surfaces the original error.
type partialWriterDelegate struct {
	sentinel error
}

func (d partialWriterDelegate) Load() error { return nil }

func (d partialWriterDelegate) Render(out io.Writer, _ string, _ any, _ ...string) error {
	if _, err := io.WriteString(out, "PARTIAL-"); err != nil {
		return err
	}
	return d.sentinel
}

func TestDelegatePartialWriteThenError(t *testing.T) {
	sentinel := errors.New("delegate failed after partial output")
	engine := newWithInner(partialWriterDelegate{sentinel: sentinel})

	var out strings.Builder
	err := engine.Render(&out, "any", nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the delegate sentinel verbatim", err)
	}
	if out.String() != "PARTIAL-" {
		t.Fatalf("writer content = %q, want the delegate's partial prefix preserved", out.String())
	}
	// the lock must be free after the failure
	if err := engine.Load(); err != nil {
		t.Fatalf("Load after partial failure: %v", err)
	}
	out.Reset()
	if err := engine.Render(&out, "any", nil); !errors.Is(err, sentinel) {
		t.Fatalf("second Render = %v, want the same sentinel (no stale state)", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("writer refused") }

// TestAppTestRouteRendersPerRequestLocale exercises the factory through a real Fiber app with
// a real c.Render (no database, no network).
func TestAppTestRouteRendersPerRequestLocale(t *testing.T) {
	app := fiber.New(fiber.Config{Views: NewHTML("./testdata", false)})
	app.Get("/probe", func(c fiber.Ctx) error {
		return c.Render("probe", fiber.Map{
			"Lang":  c.Query("lang"),
			"I18N":  c.Query("i18n"),
			"Value": c.Query("value"),
		})
	})
	for _, tc := range []struct{ lang, i18n, value string }{
		{"en", "en", "REQ-1"}, {"zh-CN", "zh-CN", "REQ-2"},
	} {
		req := httptestNewRequest(t, "/probe?lang="+tc.lang+"&i18n="+tc.i18n+"&value="+tc.value)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: app.Test: %v", tc.lang, err)
		}
		body := readBody(t, resp)
		if !strings.Contains(body, `lang="`+tc.lang+`"`) || !strings.Contains(body, `data-locale="`+tc.i18n+`"`) {
			t.Errorf("%s: body lost the request locale: %s", tc.lang, body)
		}
		if !strings.Contains(body, tc.value) {
			t.Errorf("%s: body lost the request value marker %q: %s", tc.lang, tc.value, body)
		}
	}
}
