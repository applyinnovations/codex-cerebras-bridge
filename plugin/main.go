package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/core/schemas"
)

// codexBaseInstructions must contain the base prompt from the Codex release
// matching the client version used with this gateway.
//
//go:embed codex-base-instructions.md
var codexBaseInstructions string

type ReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type ModelConfig struct {
	Slug                          string           `json:"slug"`
	Aliases                       []string         `json:"aliases,omitempty"`
	DisplayName                   string           `json:"display_name"`
	Description                   string           `json:"description"`
	ContextWindow                 int64            `json:"context_window"`
	EffectiveContextWindowPercent int64            `json:"effective_context_window_percent"`
	DefaultReasoningLevel         string           `json:"default_reasoning_level"`
	SupportedReasoningLevels      []ReasoningLevel `json:"supported_reasoning_levels"`
}

type PluginConfig struct {
	Models []ModelConfig `json:"models"`
}

type ModelMessages struct {
	InstructionsTemplate string `json:"instructions_template"`
}

type TruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

type CodexModelInfo struct {
	// Legacy field retained because older Codex clients consume it.
	BaseInstructions string `json:"base_instructions"`

	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`

	DefaultReasoningLevel    string           `json:"default_reasoning_level"`
	SupportedReasoningLevels []ReasoningLevel `json:"supported_reasoning_levels"`

	ShellType      string `json:"shell_type"`
	Visibility     string `json:"visibility"`
	SupportedInAPI bool   `json:"supported_in_api"`
	Priority       int    `json:"priority"`

	AdditionalSpeedTiers []string `json:"additional_speed_tiers"`
	ServiceTiers         []any    `json:"service_tiers"`

	AvailabilityNux any `json:"availability_nux"`
	Upgrade         any `json:"upgrade"`

	ModelMessages ModelMessages `json:"model_messages"`

	IncludeSkillsUsageInstructions bool `json:"include_skills_usage_instructions"`
	IncludePluginUsageInstructions bool `json:"include_plugin_usage_instructions"`
	IncludeAppsUsageInstructions   bool `json:"include_apps_usage_instructions"`

	SupportsReasoningSummaryParameter bool   `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary           string `json:"default_reasoning_summary"`

	SupportVerbosity bool `json:"support_verbosity"`

	TruncationPolicy TruncationPolicy `json:"truncation_policy"`

	SupportsImageDetailOriginal bool `json:"supports_image_detail_original"`

	ContextWindow    int64 `json:"context_window"`
	MaxContextWindow int64 `json:"max_context_window"`

	EffectiveContextWindowPercent int64 `json:"effective_context_window_percent"`

	ExperimentalSupportedTools []string `json:"experimental_supported_tools"`
	InputModalities            []string `json:"input_modalities"`

	SupportsSearchTool          bool `json:"supports_search_tool"`
	SupportsExperimentalContext bool `json:"supports_experimental_context"`
	UseResponsesLite            bool `json:"use_responses_lite"`

	NodeReplAutoReviewRequired bool `json:"node_repl_auto_review_required"`
	NodeReplDisabled           bool `json:"node_repl_disabled"`
}

type CodexModelsResponse struct {
	Models []CodexModelInfo `json:"models"`
}

var pluginState struct {
	sync.RWMutex
	config PluginConfig
}

func defaultConfig() PluginConfig {
	return PluginConfig{
		Models: []ModelConfig{
			{
				Slug: "cerebras/qwen-3.8-27b",
				Aliases: []string{
					"qwen-3.8-27b",
				},
				DisplayName:                   "Qwen 3.8 27B",
				Description:                   "Qwen 3.8 27B served by Cerebras",
				ContextWindow:                 131072,
				EffectiveContextWindowPercent: 95,
				DefaultReasoningLevel:         "xhigh",
				SupportedReasoningLevels: []ReasoningLevel{
					{
						Effort:      "low",
						Description: "Fast reasoning",
					},
					{
						Effort:      "medium",
						Description: "Balanced reasoning",
					},
					{
						Effort:      "xhigh",
						Description: "Deep reasoning",
					},
				},
			},
		},
	}
}

func Init(rawConfig any) error {
	cfg := defaultConfig()

	if rawConfig != nil {
		encoded, err := json.Marshal(rawConfig)
		if err != nil {
			return fmt.Errorf("encode plugin config: %w", err)
		}

		var supplied PluginConfig
		if err := json.Unmarshal(encoded, &supplied); err != nil {
			return fmt.Errorf("decode plugin config: %w", err)
		}

		if len(supplied.Models) > 0 {
			cfg.Models = supplied.Models
		}
	}

	for i := range cfg.Models {
		applyModelDefaults(&cfg.Models[i])

		if err := validateModel(cfg.Models[i]); err != nil {
			return fmt.Errorf("model %d: %w", i, err)
		}
	}

	if strings.TrimSpace(codexBaseInstructions) == "" {
		return fmt.Errorf("embedded Codex base instructions are empty")
	}

	pluginState.Lock()
	pluginState.config = cfg
	pluginState.Unlock()

	return nil
}

func GetName() string {
	return "codex-cerebras-compat"
}

func Cleanup() error {
	return nil
}

func HTTPTransportPreHook(
	ctx *schemas.BifrostContext,
	req *schemas.HTTPRequest,
) (*schemas.HTTPResponse, error) {
	if isCodexModelsRequest(req) {
		body, err := buildCodexModelsResponse()
		if err != nil {
			return nil, err
		}

		ctx.Log(
			schemas.LogLevelInfo,
			"returning Codex model metadata catalog",
		)

		return &schemas.HTTPResponse{
			StatusCode: 200,
			Headers: map[string]string{
				"Content-Type":  "application/json",
				"Cache-Control": "no-store",
			},
			Body: body,
		}, nil
	}

	if !strings.EqualFold(req.Method, "POST") ||
		!strings.HasSuffix(req.Path, "/v1/responses") {
		return nil, nil
	}

	model, inputCount, rolesBefore := responsesRequestSummary(req.Body)

	newBody, hoisted, sc, err := normalizeResponsesBody(req.Body)
	if err != nil {
		return nil, err
	}

	if sc != nil {
		ctx.Log(
			schemas.LogLevelInfo,
			fmt.Sprintf(
				"rejecting responses path=%s model=%s input=%d roles_before=%v: unsupported system/developer content",
				req.Path,
				model,
				inputCount,
				rolesBefore,
			),
		)

		return sc, nil
	}

	if hoisted > 0 {
		_, _, rolesAfter := responsesRequestSummary(newBody)
		ctx.Log(
			schemas.LogLevelInfo,
			fmt.Sprintf(
				"hoisted responses path=%s model=%s input=%d roles_before=%v hoisted=%d roles_after=%v",
				req.Path,
				model,
				inputCount,
				rolesBefore,
				hoisted,
				rolesAfter,
			),
		)

		req.Body = newBody
	}

	return nil, nil
}

func isCodexModelsRequest(req *schemas.HTTPRequest) bool {
	if !strings.EqualFold(req.Method, "GET") {
		return false
	}

	if !strings.HasSuffix(req.Path, "/v1/models") {
		return false
	}

	// Codex appends client_version to its model-catalog request.
	// Ordinary OpenAI-compatible /v1/models callers should continue through
	// Bifrost unchanged.
	return req.CaseInsensitiveQueryLookup("client_version") != ""
}

func buildCodexModelsResponse() ([]byte, error) {
	pluginState.RLock()
	cfg := pluginState.config
	pluginState.RUnlock()

	models := make([]CodexModelInfo, 0, len(cfg.Models))

	for _, model := range cfg.Models {
		models = append(models, CodexModelInfo{
			BaseInstructions: codexBaseInstructions,

			Slug:        model.Slug,
			DisplayName: model.DisplayName,
			Description: model.Description,

			DefaultReasoningLevel:    model.DefaultReasoningLevel,
			SupportedReasoningLevels: model.SupportedReasoningLevels,

			ShellType:      "unified_exec",
			Visibility:     "list",
			SupportedInAPI: true,
			Priority:       1,

			AdditionalSpeedTiers: []string{},
			ServiceTiers:         []any{},

			AvailabilityNux: nil,
			Upgrade:         nil,

			ModelMessages: ModelMessages{
				InstructionsTemplate: codexBaseInstructions,
			},

			// Mirror conservative fallback-model behavior. Codex Desktop itself
			// supplies its app/plugin/skill context separately.
			IncludeSkillsUsageInstructions: false,
			IncludePluginUsageInstructions: false,
			IncludeAppsUsageInstructions:   false,

			// Cerebras/Qwen does not expose OpenAI reasoning-summary controls
			// natively. The reasoning effort selector is still available.
			SupportsReasoningSummaryParameter: false,
			DefaultReasoningSummary:           "auto",

			SupportVerbosity: false,

			TruncationPolicy: TruncationPolicy{
				Mode:  "bytes",
				Limit: 10000,
			},

			SupportsImageDetailOriginal: false,

			ContextWindow:    model.ContextWindow,
			MaxContextWindow: model.ContextWindow,

			EffectiveContextWindowPercent: model.EffectiveContextWindowPercent,

			ExperimentalSupportedTools: []string{},
			InputModalities:            []string{"text"},

			SupportsSearchTool:          false,
			SupportsExperimentalContext: false,
			UseResponsesLite:            false,

			NodeReplAutoReviewRequired: false,
			NodeReplDisabled:           false,
		})
	}

	return json.Marshal(CodexModelsResponse{
		Models: models,
	})
}

// normalizeResponsesBody is the pure decision core for Responses request
// normalization. It scans the ENTIRE input[] and hoists every textual
// system/developer message into top-level instructions, preserving all
// other items in order.
//
// Returns:
//   - newBody:       the rewritten body, or nil when no change is needed.
//   - hoisted:       the number of messages hoisted (0 = no change).
//   - shortCircuit:  non-nil when a clear 400 *schemas.HTTPResponse must
//     be returned (unsupported system/developer content).
//   - err:           non-nil only for genuine internal/encode failures.
func normalizeResponsesBody(
	body []byte,
) (newBody []byte, hoisted int, shortCircuit *schemas.HTTPResponse, err error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Leave malformed/non-JSON requests to Bifrost's normal validation.
		return nil, 0, nil, nil
	}

	var requestedModel string
	if rawModel, ok := parsed["model"]; ok {
		var s string
		if uerr := json.Unmarshal(rawModel, &s); uerr == nil {
			requestedModel = s
		}
	}

	if !isConfiguredModel(requestedModel) {
		// Only normalize requests for the configured Qwen slugs.
		return nil, 0, nil, nil
	}

	rawInput, ok := parsed["input"]
	if !ok {
		return nil, 0, nil, nil
	}

	var input []json.RawMessage
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return nil, 0, nil, nil
	}

	instructions := ""
	instructionsIsString := true
	if rawInstructions, ok := parsed["instructions"]; ok {
		var s string
		if uerr := json.Unmarshal(rawInstructions, &s); uerr != nil {
			// Non-string instructions: skip normalization, leave body unchanged.
			instructionsIsString = false
		} else {
			instructions = s
		}
	}

	if !instructionsIsString {
		return nil, 0, nil, nil
	}

	instructionParts := make([]string, 0, len(input)+1)
	if instructions != "" {
		instructionParts = append(instructionParts, instructions)
	}

	remainingInput := make([]json.RawMessage, 0, len(input))
	hoisted = 0

	for _, rawItem := range input {
		var message struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}

		if uerr := json.Unmarshal(rawItem, &message); uerr != nil {
			// Non-object item: preserve as-is.
			remainingInput = append(remainingInput, rawItem)
			continue
		}

		if message.Role != "system" && message.Role != "developer" {
			remainingInput = append(remainingInput, rawItem)
			continue
		}

		// Only hoist ordinary message items (type omitted or "message").
		if message.Type != "" && message.Type != "message" {
			remainingInput = append(remainingInput, rawItem)
			continue
		}

		text, ok := textContent(message.Content)
		if !ok {
			// Unsupported system/developer content: return a clear 400.
			errBody, _ := json.Marshal(map[string]any{
				"error": map[string]any{
					"message": "the plugin cannot convert this system/developer message to a text instruction; please reformat the request",
					"type":    "plugin_error",
					"code":    "unsupported_instruction_content",
				},
			})

			return nil, 0, &schemas.HTTPResponse{
				StatusCode: 400,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: errBody,
			}, nil
		}

		instructionParts = append(instructionParts, text)
		hoisted++
	}

	if hoisted == 0 {
		return nil, 0, nil, nil
	}

	mergedInstructions, err := json.Marshal(strings.Join(instructionParts, "\n\n"))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("encode merged instructions: %w", err)
	}

	remainingEncoded, err := json.Marshal(remainingInput)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("encode remaining input: %w", err)
	}

	parsed["instructions"] = mergedInstructions
	parsed["input"] = remainingEncoded

	rewritten, err := json.Marshal(parsed)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("encode rewritten Responses request: %w", err)
	}

	return rewritten, hoisted, nil, nil
}

// responsesRequestSummary extracts structure-only metadata from a Responses
// request body: the requested model slug, the input item count, and the role
// of each input item. Used for logging only; prompt contents are never
// returned.
func responsesRequestSummary(body []byte) (model string, count int, roles []string) {
	var parsed struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, nil
	}

	model = parsed.Model

	if len(parsed.Input) == 0 {
		return model, 0, nil
	}

	var input []json.RawMessage
	if err := json.Unmarshal(parsed.Input, &input); err != nil {
		return model, 0, nil
	}

	return model, len(input), rolesFromInput(input)
}

// rolesFromInput returns the role of each input item in order, replacing
// non-object items and items without a role with "?".
func rolesFromInput(input []json.RawMessage) []string {
	roles := make([]string, 0, len(input))

	for _, rawItem := range input {
		var item struct {
			Role string `json:"role"`
		}

		if err := json.Unmarshal(rawItem, &item); err != nil || item.Role == "" {
			roles = append(roles, "?")
			continue
		}

		roles = append(roles, item.Role)
	}

	return roles
}

func isConfiguredModel(requested string) bool {
	pluginState.RLock()
	cfg := pluginState.config
	pluginState.RUnlock()

	for _, model := range cfg.Models {
		if requested == model.Slug {
			return true
		}

		for _, alias := range model.Aliases {
			if requested == alias {
				return true
			}
		}

		// Automatically accept the unqualified form of provider/model.
		if slash := strings.IndexByte(model.Slug, '/'); slash >= 0 &&
			requested == model.Slug[slash+1:] {
			return true
		}
	}

	return false
}

func textContent(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}

	var direct string
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, true
	}

	// Some clients wrap the content in an object with a "content" field.
	var wrapped struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return textContent(wrapped.Content)
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", false
	}

	var out strings.Builder

	for index, block := range blocks {
		switch block.Type {
		case "input_text", "text":
		default:
			return "", false
		}

		if index > 0 {
			out.WriteByte('\n')
		}

		out.WriteString(block.Text)
	}

	return out.String(), true
}

func applyModelDefaults(model *ModelConfig) {
	if model.DisplayName == "" {
		model.DisplayName = model.Slug
	}

	if model.Description == "" {
		model.Description = model.DisplayName
	}

	if model.ContextWindow <= 0 {
		model.ContextWindow = 131072
	}

	if model.EffectiveContextWindowPercent <= 0 {
		model.EffectiveContextWindowPercent = 95
	}

	if model.DefaultReasoningLevel == "" {
		model.DefaultReasoningLevel = "xhigh"
	}

	if len(model.SupportedReasoningLevels) == 0 {
		model.SupportedReasoningLevels = []ReasoningLevel{
			{
				Effort:      "low",
				Description: "Fast reasoning",
			},
			{
				Effort:      "medium",
				Description: "Balanced reasoning",
			},
			{
				Effort:      "xhigh",
				Description: "Deep reasoning",
			},
		}
	}
}

func validateModel(model ModelConfig) error {
	if model.Slug == "" {
		return fmt.Errorf("slug is required")
	}

	if model.ContextWindow <= 0 {
		return fmt.Errorf("context_window must be positive")
	}

	if model.EffectiveContextWindowPercent <= 0 ||
		model.EffectiveContextWindowPercent > 100 {
		return fmt.Errorf(
			"effective_context_window_percent must be between 1 and 100",
		)
	}

	supported := false

	for _, effort := range model.SupportedReasoningLevels {
		if effort.Effort == model.DefaultReasoningLevel {
			supported = true
		}

		switch effort.Effort {
		case "low", "medium", "xhigh":
		default:
			return fmt.Errorf(
				"unsupported Qwen reasoning effort %q",
				effort.Effort,
			)
		}
	}

	if !supported {
		return fmt.Errorf(
			"default reasoning level %q is not in supported_reasoning_levels",
			model.DefaultReasoningLevel,
		)
	}

	return nil
}
