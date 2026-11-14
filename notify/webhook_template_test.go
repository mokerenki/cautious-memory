package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file locks the **capability boundary** of the generic Webhook template.
//
// This is the only place in the package where "user-provided strings are evaluated as code", so we must be explicit
// about what it can and cannot do, and pin these properties with tests—otherwise someone might casually add a method
// to the template context, or slip a readFile into FuncMap, silently expanding the capability surface while the diff
// looks like a harmless small function.

// TestTemplateContextHasNoMethods is the most important one.
//
// text/template calls exported methods ({{.Foo}} can read a field or invoke a method). So as soon as the template context
// can reach **any** type with exported methods, those methods are all exposed to the template author.
// The context for this feature is intentionally pure data (only exported fields, zero methods).
//
// If this fails: someone added a method to webhookTemplateData / webhookItem.
// Before allowing it, think through whether that method could be used by a template to read things that shouldn't be exposed.
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s exposes %d methods (%s): text/template can call them,"+
				" equal to opening these methods' capabilities to template authors", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal locks the function set exposed to templates.
//
// Each additional function in FuncMap adds one capability. Currently only json / jsons, which serialize values
// into JSON fragments—cannot read files, cannot send requests, cannot execute commands.
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template function set changed: got %v, want %v. Before adding a function, confirm it won't expand the capability surface"+
			" (cannot read/write files, cannot make network requests, cannot execute commands)", got, want)
	}
}

// TestTemplateCannotReachUnknownData covers out-of-bounds access in templates:
// accessing nonexistent things must error, not echo something; and the error must not leak internal data.
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("accessing nonexistent fields should error")
	}
	// The error must not reveal real content from the template context (finding title/summary).
	for _, leak := range []string{"SQL注入", "参数 id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("template error leaked message content %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently a template written wrong is a config error; retry won't self-heal.
// If judged retryable, a bad template would make every delivery waste three backoff rounds.
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("template syntax error should be caught at save time")
	}
	// Even if validation is bypassed and delivery attempted directly, it must be judged permanent failure rather than retried repeatedly.
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("bad template should be judged permanent failure, got %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON covers the constraint "template render result must be valid JSON".
// It also blocks "use a template to generate plain text to trigger other protocols" usage.
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// A valid template passes.
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("valid template should pass validation: %v", err)
	}
	// Rendering non-JSON must be rejected (not sent verbatim).
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("rendering non-JSON should be judged permanent failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("error message should indicate a JSON problem, got %v", err)
	}
}
