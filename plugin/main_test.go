package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// ensureConfig loads a default config once so isConfiguredModel sees the
// configured slug across all tests.
func TestMain(m *testing.M) {
	if err := Init(nil); err != nil {
		panic(err)
	}
	code := m.Run()
	if err := Cleanup(); err != nil {
		panic(err)
	}
	_ = code
}

// itemText builds an input item with the given role and a simple input_text
// block holding the supplied text.
func itemText(role, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{
			{"type": "input_text", "text": text},
		},
	})
	return string(b)
}

// itemStringContent builds an input item whose content is a plain string.
func itemStringContent(role, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type":    "message",
		"role":    role,
		"content": text,
	})
	return string(b)
}

func responsesBody(model string, instructions string, input ...string) []byte {
	obj := map[string]any{"model": model}
	if instructions != "" {
		obj["instructions"] = instructions
	}
	arr := make([]any, 0, len(input))
	for _, s := range input {
		var v any
		_ = json.Unmarshal([]byte(s), &v)
		arr = append(arr, v)
	}
	obj["input"] = arr
	b, _ := json.Marshal(obj)
	return b
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decoded body is not valid JSON: %v", err)
	}
	return m
}

func bodyInput(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("body input is not an array: %#v", body["input"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("body input element is not an object: %#v", e)
		}
		out = append(out, m)
	}
	return out
}

func bodyRoles(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		r, _ := it["role"].(string)
		out = append(out, r)
	}
	return out
}

func assertNoSystemLater(t *testing.T, items []map[string]any) {
	t.Helper()
	for i, it := range items {
		if i == 0 {
			continue
		}
		if r, _ := it["role"].(string); r == "system" || r == "developer" {
			t.Fatalf("found a %q message at index %d; provider requires a single leading system", r, i)
		}
	}
}

// --- Cases 1-4: hoisting behavior --------------------------------------

func Test_Hoist_DeveloperUser(t *testing.T) {
	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("developer", "sys one"),
		itemText("user", "hi there"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc != nil {
		t.Fatalf("unexpected short-circuit: %d %s", sc.StatusCode, string(sc.Body))
	}
	if hoisted != 1 {
		t.Fatalf("expected 1 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	instr, _ := m["instructions"].(string)
	if !strings.Contains(instr, "sys one") {
		t.Fatalf("instructions missing hoisted dev text: %q", instr)
	}
	items := bodyInput(t, m)
	if got := bodyRoles(items); len(got) != 1 || got[0] != "user" {
		t.Fatalf("expected [user], got %v", got)
	}
	assertNoSystemLater(t, items)
}

func Test_Hoist_UserDeveloperUser(t *testing.T) {
	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("user", "first"),
		itemText("developer", "mid system"),
		itemText("user", "second"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 1 {
		t.Fatalf("expected 1 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	items := bodyInput(t, m)
	if got := bodyRoles(items); len(got) != 2 || got[0] != "user" || got[1] != "user" {
		t.Fatalf("expected [user user], got %v", got)
	}
	assertNoSystemLater(t, items)
}

func Test_Hoist_UserDeveloperUserSystemUser(t *testing.T) {
	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("user", "a"),
		itemText("developer", "dev mid"),
		itemText("user", "b"),
		itemText("system", "sys mid"),
		itemText("user", "c"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 2 {
		t.Fatalf("expected 2 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	instr, _ := m["instructions"].(string)
	if !strings.Contains(instr, "dev mid") || !strings.Contains(instr, "sys mid") {
		t.Fatalf("instructions missing hoisted text: %q", instr)
	}
	items := bodyInput(t, m)
	if got := bodyRoles(items); len(got) != 3 || got[0] != "user" || got[1] != "user" || got[2] != "user" {
		t.Fatalf("expected [user user user], got %v", got)
	}
	assertNoSystemLater(t, items)
}

func Test_Hoist_MultipleDeveloper(t *testing.T) {
	body := responsesBody("qwen-3.8-27b", "",
		itemText("developer", "da"),
		itemText("user", "u"),
		itemText("developer", "db"),
		itemText("developer", "dc"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 3 {
		t.Fatalf("expected 3 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	instr, _ := m["instructions"].(string)
	for _, frag := range []string{"da", "db", "dc"} {
		if !strings.Contains(instr, frag) {
			t.Fatalf("instructions missing %q: %q", frag, instr)
		}
	}
	items := bodyInput(t, m)
	if got := bodyRoles(items); len(got) != 1 || got[0] != "user" {
		t.Fatalf("expected [user], got %v", got)
	}
}

// --- Case 5: normal user,assistant,user unchanged ----------------------

func Test_NoChange_UserAssistantUser(t *testing.T) {
	orig := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("user", "one"),
		itemText("assistant", "two"),
		itemText("user", "three"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc != nil {
		t.Fatalf("unexpected short-circuit")
	}
	if hoisted != 0 {
		t.Fatalf("expected 0 hoisted, got %d", hoisted)
	}
	if newBody != nil {
		t.Fatalf("expected nil newBody when nothing hoisted")
	}
}

// --- Case 6: unrelated model unchanged ---------------------------------

func Test_UnrelatedModel_Unchanged(t *testing.T) {
	orig := responsesBody("gpt-4o", "",
		itemText("developer", "should not be hoisted"),
		itemText("user", "hi"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(orig)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 0 {
		t.Fatalf("expected 0 hoisted for unrelated model, got %d", hoisted)
	}
	if newBody != nil {
		t.Fatalf("expected nil newBody for unrelated model")
	}
}

// --- Case 7: unsupported instruction content fails clearly --------------

func Test_UnsupportedContent_FailsClearly(t *testing.T) {
	// system message with a non-textual content block (image) -> 400.
	imgItem := itemStringContentUnused()
	_ = imgItem
	body := responsesBody("cerebras/qwen-3.8-27b", "",
		imgItem,
		itemText("user", "hi"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil {
		t.Fatalf("did not expect transport error: %v", err)
	}
	if sc == nil {
		t.Fatalf("expected a 400 short-circuit for unsupported content, nil; hoisted=%d", hoisted)
	}
	if sc.StatusCode != 400 {
		t.Fatalf("expected status 400, got %d", sc.StatusCode)
	}
	if !strings.Contains(string(sc.Body), "unsupported_instruction_content") {
		t.Fatalf("expected clear error code in body, got: %s", string(sc.Body))
	}
	if newBody != nil {
		t.Fatalf("expected nil newBody on short-circuit")
	}
}

// itemStringContentUnused builds a system message whose content is an image
// block (not text), which must trigger the clear 400 path.
func itemStringContentUnused() string {
	b, _ := json.Marshal(map[string]any{
		"type": "message",
		"role": "system",
		"content": []map[string]any{
			{"type": "input_image", "image_url": "https://example.com/x.png"},
		},
	})
	return string(b)
}

// --- Order preservation of non-instruction history ----------------------

func Test_PreservesNonInstructionOrder(t *testing.T) {
	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("user", "u1"),
		itemText("assistant", "a1"),
		itemText("developer", "dev1"),
		itemText("user", "u2"),
		itemText("assistant", "a2"),
		itemText("system", "sys1"),
		itemText("user", "u3"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 2 {
		t.Fatalf("expected 2 hoisted, got %d", hoisted)
	}
	items := bodyInput(t, decodeBody(t, newBody))
	want := []string{"user", "assistant", "user", "assistant", "user"}
	if got := bodyRoles(items); len(got) != len(want) {
		t.Fatalf("roles length mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got := bodyRoles(items)[i]; got != want[i] {
			t.Fatalf("role at %d: got %q want %q (full=%v)", i, got, want[i], bodyRoles(items))
		}
	}
	assertNoSystemLater(t, items)
}

// --- Existing string instructions become the first merged part ----------

func Test_ExistingInstructionsBecomesFirstPart(t *testing.T) {
	body := responsesBody("cerebras/qwen-3.8-27b", "base instruction",
		itemText("user", "hi"),
		itemText("developer", "extra dev"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 1 {
		t.Fatalf("expected 1 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	instr, _ := m["instructions"].(string)
	baseIdx := strings.Index(instr, "base instruction")
	devIdx := strings.Index(instr, "extra dev")
	if baseIdx == -1 || devIdx == -1 || baseIdx > devIdx {
		t.Fatalf("expected base instruction first then dev text; got %q", instr)
	}
}

// --- String content shape is hoisted ------------------------------------

func Test_StringContentShape(t *testing.T) {
	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemStringContent("system", "plain string sys"),
		itemText("user", "hi"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil || sc != nil {
		t.Fatalf("err=%v sc=%v", err, sc)
	}
	if hoisted != 1 {
		t.Fatalf("expected 1 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	instr, _ := m["instructions"].(string)
	if !strings.Contains(instr, "plain string sys") {
		t.Fatalf("instructions missing string content: %q", instr)
	}
	items := bodyInput(t, m)
	if got := bodyRoles(items); len(got) != 1 || got[0] != "user" {
		t.Fatalf("expected [user], got %v", got)
	}
}

// --- textContent covers both block types --------------------------------

func Test_TextContent_BlockTypes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"string", `{"type":"x","content":"hello"}`, "hello", true},
		{"input_text", `[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]`, "a\nb", true},
		{"text", `[{"type":"text","text":"z"}]`, "z", true},
		{"mixed", `[{"type":"input_text","text":"a"},{"type":"text","text":"b"}]`, "a\nb", true},
		{"image", `[{"type":"input_image","image_url":"x"}]`, "", false},
		{"null", `null`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := textContent(json.RawMessage(tc.raw))
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (got %q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("content=%q want %q", got, tc.want)
			}
		})
	}
}

// --- isConfiguredModel matches slug + unqualified form ------------------

func Test_IsConfiguredModel(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"cerebras/qwen-3.8-27b", true},
		{"qwen-3.8-27b", true},
		{"cerebras/gpt-oss-120b", true},
		{"gpt-oss-120b", true},
		{"gpt-4o", false},
		{"", false},
		{"other/model", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isConfiguredModel(tc.in); got != tc.want {
				t.Fatalf("isConfiguredModel(%q)=%v want %v", tc.in, got, tc.want)
			}
		})
	}
}

// --- gpt-oss-120b: hoisting applies to the second configured model ---

func Test_Hoist_GptOss_DeveloperUser(t *testing.T) {
	body := responsesBody("cerebras/gpt-oss-120b", "",
		itemText("developer", "oss sys"),
		itemText("user", "hello oss"),
	)
	newBody, hoisted, sc, err := normalizeResponsesBody(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc != nil {
		t.Fatalf("unexpected short-circuit: %d %s", sc.StatusCode, string(sc.Body))
	}
	if hoisted != 1 {
		t.Fatalf("expected 1 hoisted, got %d", hoisted)
	}
	m := decodeBody(t, newBody)
	instr, _ := m["instructions"].(string)
	if !strings.Contains(instr, "oss sys") {
		t.Fatalf("instructions missing hoisted dev text: %q", instr)
	}
	items := bodyInput(t, m)
	if got := bodyRoles(items); len(got) != 1 || got[0] != "user" {
		t.Fatalf("expected [user], got %v", got)
	}
	assertNoSystemLater(t, items)
}

// --- validateModel: high effort accepted, bogus effort rejected --------

func Test_ValidateModel_ReasoningEfforts(t *testing.T) {
	mk := func(defaultLevel string, efforts ...string) ModelConfig {
		model := ModelConfig{
			Slug:                          "cerebras/gpt-oss-120b",
			DisplayName:                   "GPT-OSS-120B",
			ContextWindow:                 131072,
			EffectiveContextWindowPercent: 95,
			DefaultReasoningLevel:         defaultLevel,
		}
		for _, e := range efforts {
			model.SupportedReasoningLevels = append(
				model.SupportedReasoningLevels,
				ReasoningLevel{Effort: e, Description: e},
			)
		}
		return model
	}

	if err := validateModel(
		mk("medium", "low", "medium", "high"),
	); err != nil {
		t.Fatalf("gpt-oss config (low/medium/high) should validate: %v", err)
	}
	if err := validateModel(
		mk("xhigh", "low", "medium", "xhigh"),
	); err != nil {
		t.Fatalf("qwen config (low/medium/xhigh) should validate: %v", err)
	}
	if err := validateModel(mk("medium", "low", "banana", "high")); err == nil {
		t.Fatalf("bogus effort %q should be rejected", "banana")
	}
	if err := validateModel(mk("high", "low", "medium")); err == nil {
		t.Fatal("default level not in supported list should be rejected")
	}
}

// --- Hook logging: hoist writes a structure-only log -------------------
//
// The hook must produce a log line containing the path, model, input count,
// hoisted count, and role lists (before/after) — and nothing like prompt
// content.

func Test_Hook_Logs_Hoist(t *testing.T) {
	root := schemas.NewBifrostContext(context.Background(), time.Now())
	name := GetName()
	scoped := root.WithPluginScope(&name)
	defer scoped.ReleasePluginScope()

	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("user", "one"),
		itemText("developer", "dev"),
		itemText("user", "two"),
	)
	req := &schemas.HTTPRequest{
		Method: "POST",
		Path:   "/v1/responses",
		Body:   body,
	}

	resp, err := HTTPTransportPreHook(scoped, req)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil response (passthrough), got %d", resp.StatusCode)
	}

	logs := root.GetPluginLogs()
	if len(logs) == 0 {
		t.Fatalf("expected a hoist log entry, got none")
	}
	last := logs[len(logs)-1].Message
	if !strings.Contains(last, "hoisted responses") {
		t.Fatalf("expected hoist log, got %q", last)
	}
	for _, needle := range []string{
		"model=cerebras/qwen-3.8-27b",
		"input=3",
		"hoisted=1",
		"roles_before=[user developer user]",
		"roles_after=[user user]",
	} {
		if !strings.Contains(last, needle) {
			t.Fatalf("log missing %q in %q", needle, last)
		}
	}
	// The hoisted developer text ("dev" as a standalone item value) must
	// not leak into the log; "developer" only appears as a role name.
	if strings.Contains(last, " dev ") {
		t.Fatalf("log must be structure-only; prompt text leaked: %q", last)
	}
}

// --- Hook rejects unsupported content with a 400 short-circuit ----------

func Test_Hook_ShortCircuit_Unsupported(t *testing.T) {
	root := schemas.NewBifrostContext(context.Background(), time.Now())
	name := GetName()
	scoped := root.WithPluginScope(&name)
	defer scoped.ReleasePluginScope()

	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemStringContentUnused(),
		itemText("user", "hi"),
	)
	req := &schemas.HTTPRequest{Method: "POST", Path: "/v1/responses", Body: body}

	resp, err := HTTPTransportPreHook(scoped, req)
	if err != nil {
		t.Fatalf("expected short-circuit response, not error: %v", err)
	}
	if resp == nil || resp.StatusCode != 400 {
		t.Fatalf("expected 400 short-circuit, got resp=%v", resp)
	}
}

// --- Hook is a no-op for non-Codex models and non-responses paths -------

func Test_Hook_Noop_NonResponsesPath(t *testing.T) {
	root := schemas.NewBifrostContext(context.Background(), time.Now())
	name := GetName()
	scoped := root.WithPluginScope(&name)
	defer scoped.ReleasePluginScope()

	body := responsesBody("cerebras/qwen-3.8-27b", "",
		itemText("developer", "x"),
	)
	orig := string(body)
	req := &schemas.HTTPRequest{Method: "POST", Path: "/v1/chat/completions", Body: body}
	resp, err := HTTPTransportPreHook(scoped, req)
	if err != nil || resp != nil {
		t.Fatalf("expected no-op, got resp=%v err=%v", resp, err)
	}
	if string(req.Body) != orig {
		t.Fatalf("hook mutated body on non-responses path")
	}
}
